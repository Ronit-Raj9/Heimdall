package toolproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// External routes front real HTTP APIs outside the bench
// (docs/external-apis.md). The agent reaches API <id> at /x/<id>/<path>.
// The proxy is the only place such a call can leave from: it allows only
// the methods and path prefixes on the route's allowlist, never lets the
// agent pick the host, strips the agent's own credentials, injects the
// route's credential, caps calls, sizes and time, and records every call.
// In record mode each answered call is also appended to a cassette; in
// replay mode calls are answered from the cassette and the proxy opens no
// connection at all.

// StatusUnmatched is what a replayed call with no recorded answer gets. It
// is outside the range real servers use, so an agent (or a person reading
// the ledger) can tell it from an answer of the API.
const StatusUnmatched = 599

// redacted replaces the route's credential wherever it would be written.
const redacted = "[REDACTED]"

// externalStrip are request headers the agent may not send upstream: its own
// credentials and cookies, and headers that would describe the proxy.
var externalStrip = []string{"Authorization", "Proxy-Authorization", "Cookie", "Accept-Encoding", "Host", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip"}

// externalRoute is the running state of one external route.
type externalRoute struct {
	route    Route
	cfg      ExternalRoute
	base     *url.URL
	client   *http.Client // nil in replay mode: no connection is ever opened
	secret   string       // the injected credential; empty in replay mode
	authHead string
	tape     *cassette       // replay
	rec      *cassetteWriter // record
	maxCalls int
	maxBytes int64
	timeout  time.Duration

	mu    sync.Mutex
	calls int
}

// newExternalRoute builds the state of route r: in replay mode it loads the
// cassette (a missing or broken one stops the proxy from starting), in
// record mode it creates the cassette file, and in live and record mode it
// reads the credential and builds a client that dials Connect when set.
func newExternalRoute(cfg Config, r Route) (*externalRoute, error) {
	if r.External == nil {
		return nil, fmt.Errorf("route %s: external route has no external block", r.Service)
	}
	base, err := url.Parse(r.Upstream)
	if err != nil {
		return nil, fmt.Errorf("route %s: %w", r.Service, err)
	}
	x := &externalRoute{route: r, cfg: *r.External, base: base, maxCalls: r.External.MaxCalls, maxBytes: r.External.MaxBytes,
		timeout: time.Duration(r.External.TimeoutS) * time.Second, authHead: http.CanonicalHeaderKey(r.External.AuthHeader)}
	if x.maxCalls == 0 {
		x.maxCalls = utils.DefaultExternalMaxCalls
	}
	if x.maxBytes == 0 {
		x.maxBytes = utils.DefaultExternalMaxBytes
	}
	if x.timeout == 0 {
		x.timeout = utils.DefaultExternalTimeoutS * time.Second
	}
	if x.authHead == "" {
		x.authHead = utils.DefaultExternalAuthHeader
	}
	switch x.cfg.Mode {
	case schemas.ExternalReplay:
		if x.tape, err = loadCassette(x.cfg.Cassette); err != nil {
			return nil, fmt.Errorf("route %s: %w", r.Service, err)
		}
		return x, nil
	case schemas.ExternalRecord:
		if x.rec, err = openCassetteWriter(x.cfg.RecordTo); err != nil {
			return nil, fmt.Errorf("route %s: %w", r.Service, err)
		}
	}
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if x.cfg.AuthEnv != "" {
		x.secret = getenv(x.cfg.AuthEnv)
		if x.secret == "" {
			cfg.Logger.Warn("external api credential variable is empty; calls go without it", "service", r.Service, "auth_env", x.cfg.AuthEnv)
		}
	}
	x.client = externalClient(base, x.cfg.Connect, cfg.RootCAs)
	return x, nil
}

// externalClient dials connect (when set) in place of the base URL's host,
// keeps the base URL's host as the TLS server name, and never follows a
// redirect: a 3xx goes back to the agent as it came.
func externalClient(base *url.URL, connect string, roots *x509.CertPool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if connect != "" {
				addr = connect
			}
			return d.DialContext(ctx, network, addr)
		},
		TLSClientConfig:     &tls.Config{ServerName: base.Hostname(), RootCAs: roots, MinVersion: tls.VersionTLS12},
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// close closes the record cassette.
func (x *externalRoute) close() error {
	if x.rec == nil {
		return nil
	}
	return x.rec.Close()
}

// serveExternal handles one call to /x/<id>/<path>. agent is the team
// member whose route (/m/<token>/x/...) the call came in on, or "".
func (s *Server) serveExternal(w http.ResponseWriter, r *http.Request, rest, agent string) {
	id, _, _ := strings.Cut(rest, "/")
	x, ok := s.external[id]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown external api %q", id), http.StatusNotFound)
		return
	}
	started := s.now()
	path, bad := externalPath(r, id)
	row := schemas.ToolCall{Server: id, Tool: x.redact(r.Method + " " + path), Protocol: ProtocolExternal, Started: started, AgentID: agent}
	if bad != "" {
		s.externalRefuse(w, x, &row, http.StatusBadRequest, bad)
		return
	}
	if !x.allowed(r.Method, path) {
		s.externalRefuse(w, x, &row, http.StatusForbidden, fmt.Sprintf("%s %s is not on the allowlist of external api %s", r.Method, path, id))
		return
	}
	if !x.take() {
		s.externalRefuse(w, x, &row, http.StatusTooManyRequests, fmt.Sprintf("external api %s is over its cap of %d calls for this run", id, x.maxCalls))
		return
	}
	body, err := readUpTo(r.Body, x.maxBytes)
	if errors.Is(err, errTooLarge) {
		s.externalRefuse(w, x, &row, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body is over the %d byte cap of external api %s", x.maxBytes, id))
		return
	}
	if err != nil {
		s.externalRefuse(w, x, &row, http.StatusBadRequest, "read request: "+err.Error())
		return
	}
	args := body
	if r.URL.RawQuery != "" {
		args = []byte("?" + r.URL.RawQuery)
		if len(body) > 0 {
			args = append(append(args, '\n'), body...)
		}
	}
	row.Arguments, row.ArgsSHA256 = Capture(x.redactBytes(args))
	ft, hit := s.faults.decide(id, row.Tool, agent) // Multi-agent area: a member's route names the member
	if hit && ft.Mode == FaultInputDrift { // lane X22: a new-shape call is translated and forwarded
		nb, stale := driftCall(ft, body)
		if !stale {
			hit, body = false, nb
		}
	}
	if hit && (ft.Mode == FaultError || ft.Mode == FaultTimeout) {
		s.externalFault(w, r, &row, ft)
		return
	}
	if hit && (answersItself(ft.Mode) || ft.Mode == FaultInputDrift) { // lane X22
		s.syntheticHTTP(w, &row, ft)
		return
	}
	if hit && ft.Mode == FaultSlow { // lane X22
		row.Fault, row.Nonce = ft.Mode, ft.Nonce
		if err := s.hold(r.Context(), slowHold(ft)); err != nil {
			row.Undelivered = true
			row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
			s.append(&row)
			return
		}
	}
	if hit && ft.Mode == FaultDuplicateEffect && x.tape == nil { // lane X22: a live call is sent twice; a replay has no effect to repeat
		row.Fault, row.Nonce, row.Effects = ft.Mode, ft.Nonce, 2
		s.externalDuplicate(r, x, path, body)
	}
	var poison *schemas.ToolFault
	if hit && (ft.Mode == FaultPoison || ft.Mode == FaultInflate || IsQuietMode(ft.Mode)) { // lost_ack is not applied to external routes (S1)
		poison = &ft
	}
	if x.tape != nil {
		s.externalReplay(w, r, x, &row, path, body, poison)
		return
	}
	s.externalLive(w, r, x, &row, path, body, poison)
}

// externalLive sends one call to the real host, relays and records the
// answer, and in record mode appends it to the cassette.
func (s *Server) externalLive(w http.ResponseWriter, r *http.Request, x *externalRoute, row *schemas.ToolCall, path string, body []byte, poison *schemas.ToolFault) {
	ctx, cancel := context.WithTimeout(r.Context(), x.timeout)
	defer cancel()
	resp, err := x.client.Do(x.request(ctx, r, path, body))
	if err != nil {
		status, msg := http.StatusBadGateway, "external api unreachable: "+transportError(err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, msg = http.StatusGatewayTimeout, fmt.Sprintf("external api %s did not answer within %s", row.Server, x.timeout)
		}
		s.externalError(w, x, row, status, x.redact(msg))
		return
	}
	defer resp.Body.Close()
	raw, err := readUpTo(resp.Body, x.maxBytes)
	switch {
	case errors.Is(err, errTooLarge):
		row.Blocked = true
		s.externalError(w, x, row, http.StatusBadGateway, fmt.Sprintf("answer of external api %s is over its %d byte cap", row.Server, x.maxBytes))
		return
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.externalError(w, x, row, http.StatusGatewayTimeout, fmt.Sprintf("external api %s did not answer within %s", row.Server, x.timeout))
		return
	case err != nil:
		s.externalError(w, x, row, http.StatusBadGateway, x.redact("read external api answer: "+err.Error()))
		return
	}
	if enc := contentEncoding(resp.Header); enc != "" {
		// The answer is decoded so it can be redacted, recorded and relayed
		// as plain bytes; a coding the proxy cannot read is refused rather
		// than relayed (or recorded) without its Content-Encoding.
		d, derr := decodeBody(raw, enc, x.maxBytes)
		switch {
		case errors.Is(derr, errDecodedTooLarge):
			row.Blocked = true
			s.externalError(w, x, row, http.StatusBadGateway, fmt.Sprintf("decoded answer of external api %s is over its %d byte cap", row.Server, x.maxBytes))
			return
		case derr != nil:
			s.externalError(w, x, row, http.StatusBadGateway, x.redact(fmt.Sprintf("answer of external api %s has content encoding %q the proxy cannot decode: %s", row.Server, enc, derr.Error())))
			return
		}
		raw = d
	}
	raw = x.redactBytes(raw)
	if int64(len(raw)) > x.maxBytes {
		// Redaction can lengthen an answer; the cap holds on the bytes
		// relayed and recorded, as replay checks them.
		row.Blocked = true
		s.externalError(w, x, row, http.StatusBadGateway, fmt.Sprintf("answer of external api %s is over its %d byte cap", row.Server, x.maxBytes))
		return
	}
	h := x.redactHeader(resp.Header)
	if x.rec != nil {
		if err := x.rec.Append(NewCassetteEntry(r.Method, path, r.URL.RawQuery, body, resp.StatusCode, h, raw)); err != nil {
			// The writer keeps the error, moves the cassette aside and
			// fails Close, so the capture is never published as complete.
			s.logger.Error("external cassette write failed", "service", row.Server, "error", err.Error())
		}
	}
	s.externalAnswer(w, x, row, resp.StatusCode, h, raw, poison)
}

// redactHeader returns a copy of an upstream answer's headers fit to relay
// and to record: the credential replaced in every value, and the content
// coding and length dropped because the body is relayed decoded. Both the
// cassette and the agent's reply are built from it, so neither can keep a
// credential the upstream echoed in a header.
func (x *externalRoute) redactHeader(src http.Header) http.Header {
	h := make(http.Header, len(src))
	for k, vs := range src {
		out := make([]string, len(vs))
		for i, v := range vs {
			out[i] = x.redact(v)
		}
		h[k] = out
	}
	h.Del("Content-Encoding")
	h.Del("Content-Length")
	return h
}

// externalReplay answers one call from the cassette, with no network. A call
// the cassette does not hold gets StatusUnmatched: it is the agent's call,
// not an infrastructure failure, and the ledger row says unmatched.
func (s *Server) externalReplay(w http.ResponseWriter, r *http.Request, x *externalRoute, row *schemas.ToolCall, path string, body []byte, poison *schemas.ToolFault) {
	e, ok := x.tape.match(r.Method, path, r.URL.RawQuery, body)
	if !ok {
		row.Unmatched = true
		q := ""
		if r.URL.RawQuery != "" {
			q = "?" + r.URL.RawQuery
		}
		s.externalError(w, x, row, StatusUnmatched, x.redact(fmt.Sprintf("external api %s is in replay mode and its cassette holds no answer for %s %s%s with this body", row.Server, r.Method, path, q)))
		return
	}
	raw, err := e.BodyBytes()
	if err != nil {
		row.Unmatched = true
		s.externalError(w, x, row, StatusUnmatched, err.Error())
		return
	}
	if int64(len(raw)) > x.maxBytes {
		// The route's answer cap holds in replay as in live mode: a
		// hand-written answer, or one recorded under a larger cap, is
		// refused the same way.
		row.Blocked, row.Replayed = true, true
		s.externalError(w, x, row, http.StatusBadGateway, fmt.Sprintf("answer of external api %s is over its %d byte cap", row.Server, x.maxBytes))
		return
	}
	h := http.Header{}
	for k, v := range e.Headers {
		h.Set(k, v)
	}
	row.Replayed = true
	s.externalAnswer(w, x, row, e.Status, h, x.redactBytes(raw), poison)
}

// externalAnswer relays an answer (live or replayed), poisoned when a poison
// fault took the call, and records it.
func (s *Server) externalAnswer(w http.ResponseWriter, x *externalRoute, row *schemas.ToolCall, status int, h http.Header, raw []byte, poison *schemas.ToolFault) {
	if poison != nil {
		var changed bool
		if raw, changed = rewriteHTTPAnswer(raw, h.Get("Content-Type"), *poison); changed {
			row.Fault, row.Nonce = poison.Mode, poison.Nonce
		}
	}
	out := w.Header()
	copyHeaders(out, h, true)
	out.Del("Set-Cookie")
	out.Del("Content-Encoding")
	for k, vs := range out {
		for i := range vs {
			vs[i] = x.redact(vs[i])
		}
		out[k] = vs
	}
	row.Status, row.IsError = status, status >= 400
	row.Result, row.ResultSHA256 = Capture(raw)
	w.WriteHeader(status)
	_, werr := w.Write(raw)
	row.Undelivered = werr != nil
	row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
	s.append(row)
}

// externalFault answers a call an error or timeout fault took, without
// reaching the API or the cassette.
func (s *Server) externalFault(w http.ResponseWriter, r *http.Request, row *schemas.ToolCall, ft schemas.ToolFault) {
	row.Fault, row.Nonce, row.IsError = ft.Mode, ft.Nonce, true
	status := http.StatusInternalServerError
	if ft.Mode == FaultTimeout {
		status = http.StatusGatewayTimeout
		if err := s.hold(r.Context(), time.Duration(holdSeconds(ft))*time.Second); err != nil {
			row.Undelivered = true
			row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
			s.append(row)
			return
		}
	}
	out, _ := json.Marshal(map[string]string{"error": faultMessage(ft)})
	row.Status = status
	row.Result, row.ResultSHA256 = Capture(out)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, werr := w.Write(out)
	row.Undelivered = werr != nil
	row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
	s.append(row)
}

// externalRefuse answers a call the proxy will not make (bad path, not on
// the allowlist, over a cap) and records it as blocked.
func (s *Server) externalRefuse(w http.ResponseWriter, x *externalRoute, row *schemas.ToolCall, status int, msg string) {
	row.Blocked = true
	if row.ArgsSHA256 == "" {
		row.ArgsSHA256 = sha256Hex(nil)
	}
	s.externalError(w, x, row, status, x.redact(msg))
}

// externalError answers with a JSON error and records the row.
func (s *Server) externalError(w http.ResponseWriter, _ *externalRoute, row *schemas.ToolCall, status int, msg string) {
	out, _ := json.Marshal(map[string]string{"error": msg})
	row.Status, row.IsError = status, true
	row.Result, row.ResultSHA256 = Capture(out)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, werr := w.Write(out)
	row.Undelivered = werr != nil
	row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
	s.append(row)
}

// request builds the upstream request: the base URL's scheme and host, the
// base path plus the call's path, the agent's query, the agent's headers
// without hop-by-hop ones and without its credentials, then the route's
// static headers and the injected credential.
func (x *externalRoute) request(ctx context.Context, r *http.Request, path string, body []byte) *http.Request {
	u := *x.base
	u.Path, u.RawPath = strings.TrimRight(x.base.Path, "/")+path, ""
	u.RawQuery = r.URL.RawQuery
	u.Fragment, u.User = "", nil
	req := (&http.Request{
		Method:        r.Method,
		URL:           &u,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Host:          u.Host,
	}).WithContext(ctx)
	if len(body) == 0 {
		req.Body, req.ContentLength = http.NoBody, 0
	}
	copyHeaders(req.Header, r.Header, true)
	for _, h := range externalStrip {
		req.Header.Del(h)
	}
	req.Header.Del(x.authHead)
	req.Header.Del(MemberHeader) // the member id stays inside the bench
	for k, v := range x.cfg.Headers {
		req.Header.Set(k, v)
	}
	if x.secret != "" {
		v := x.secret
		if x.cfg.AuthScheme != "" {
			v = x.cfg.AuthScheme + " " + v
		}
		req.Header.Set(x.authHead, v)
	}
	return req
}

// allowed reports whether method and path match an allowlist entry. A
// prefix matches on a path segment boundary: /rates allows /rates and
// /rates/today, not /rates-admin. An entry without methods allows GET.
func (x *externalRoute) allowed(method, path string) bool {
	for _, a := range x.cfg.Allow {
		methods := a.Methods
		if len(methods) == 0 {
			methods = []string{http.MethodGet}
		}
		okMethod := false
		for _, m := range methods {
			okMethod = okMethod || m == method
		}
		if okMethod && prefixMatch(path, a.PathPrefix) {
			return true
		}
	}
	return false
}

// take counts one call and reports whether it is within the cap.
func (x *externalRoute) take() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.calls++
	return x.calls <= x.maxCalls
}

// redact replaces the credential in s.
func (x *externalRoute) redact(s string) string {
	if x.secret == "" {
		return s
	}
	return strings.ReplaceAll(s, x.secret, redacted)
}

// redactBytes replaces the credential in b.
func (x *externalRoute) redactBytes(b []byte) []byte {
	if x.secret == "" || !bytes.Contains(b, []byte(x.secret)) {
		return b
	}
	return bytes.ReplaceAll(b, []byte(x.secret), []byte(redacted))
}

// externalPath returns the call's path after /x/<id> and, when the request
// must be refused, why. It refuses an absolute-form request target (a
// request that names its own host), encoded slashes, backslashes and dots,
// empty and dot segments, and anything that looks like a URL, so the path
// can only ever extend the base URL's path on the base URL's host.
func externalPath(r *http.Request, id string) (string, string) {
	if r.URL.Host != "" || r.URL.Scheme != "" || r.URL.Opaque != "" {
		return "/", "absolute-form request targets are refused: the proxy picks the host"
	}
	esc := r.URL.EscapedPath()
	rest, ok := strings.CutPrefix(esc, utils.ExternalPathPrefix+id)
	if !ok {
		return "/", "encoded characters are refused in the external api id"
	}
	if rest == "" {
		rest = "/"
	}
	low := strings.ToLower(rest)
	for _, bad := range []string{"%2f", "%5c", "%2e", "%00", "%25"} {
		if strings.Contains(low, bad) {
			return "/", "encoded slashes, backslashes, dots and percent signs are refused in external api paths"
		}
	}
	path, err := url.PathUnescape(rest)
	if err != nil {
		return "/", "path does not decode: " + err.Error()
	}
	switch {
	case !strings.HasPrefix(path, "/"):
		return "/", "path must start with /"
	case strings.Contains(path, "\\") || strings.Contains(path, "://") || strings.Contains(path, "//"):
		return path, "backslashes, // and URLs are refused in external api paths"
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "." || seg == ".." {
			return path, "dot segments are refused in external api paths"
		}
	}
	for _, c := range path {
		if c < 0x20 || c == 0x7f {
			return "/", "control characters are refused in external api paths"
		}
	}
	return path, ""
}

// prefixMatch reports whether path starts with prefix on a segment boundary.
func prefixMatch(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	return len(path) == len(prefix) || strings.HasSuffix(prefix, "/") || path[len(prefix)] == '/' || path[len(prefix)] == '?'
}

// externalDuplicate sends a live external call once more before the real
// one (duplicate_effect). It counts against the route's call cap like any
// call, and its answer is drained and dropped.
func (s *Server) externalDuplicate(r *http.Request, x *externalRoute, path string, body []byte) {
	if !x.take() {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), x.timeout)
	defer cancel()
	resp, err := x.client.Do(x.request(ctx, r, path, body))
	if err != nil {
		s.logger.Warn("duplicate_effect: the extra external send failed", "error", x.redact(transportError(err)))
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, x.maxBytes))
	resp.Body.Close()
}
