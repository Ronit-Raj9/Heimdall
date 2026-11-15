// Package toolproxy is the per-run tool proxy (docs/scenarios-design.md 4,
// docs/tool-proxy.md). It sits between the agent and the run's tool and MCP
// services: the agent reaches service <id> at /s/<id>/<path> and the proxy
// forwards to the service. Every tool call is appended to a ledger
// (tools.jsonl, one schemas.ToolCall per line) that the agent cannot reach,
// and a fault read from fault.json can answer matching calls with an error,
// hold them until they time out, or append text to a real result.
//
// The proxy uses net/http, like the fault proxy, because it streams: MCP
// servers may answer a POST with a server-sent event stream. It never crashes
// on traffic it does not understand: a body that is not JSON-RPC is passed
// through unrecorded, and a call whose answer never arrives is recorded as
// failed.
package toolproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxBody caps a request or response body the proxy reads into memory to
// parse. A larger body is passed through as a stream.
const maxBody = 32 << 20

// Config is what the proxy needs to start.
type Config struct {
	Listen     string // e.g. :9200
	RoutesPath string // routes.json
	FaultPath  string // fault.json; may be absent at start and come and go
	LedgerPath string // tools.jsonl
	Logger     schemas.Logger
	// RootCAs, when set, replaces the system roots external routes trust
	// (tests). Getenv, when set, replaces os.Getenv for external route
	// credentials (tests).
	RootCAs *x509.CertPool
	Getenv  func(string) string
}

// Server is a running tool proxy.
type Server struct {
	cfg     Config
	runID   string
	routes  map[string]Route
	targets map[string]*url.URL // parsed upstream of each route
	ledger  *Ledger
	faults  *faultFile
	client  *http.Client
	logger  schemas.Logger
	now     func() time.Time
	hold    func(ctx context.Context, d time.Duration) error

	// external holds the external routes (external.go); they are never in
	// routes, so /s/<id> cannot reach them.
	external map[string]*externalRoute
}

// New loads the routes, opens the ledger and returns a server ready to
// serve. Without valid routes or a writable ledger the proxy refuses to
// start: the engine reads that as an infrastructure failure.
func New(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	rs, err := LoadRoutes(cfg.RoutesPath)
	if err != nil {
		return nil, err
	}
	return newServer(cfg, rs)
}

// newServer builds a server from parsed routes.
func newServer(cfg Config, rs *Routes) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	l, err := OpenLedger(cfg.LedgerPath)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:     cfg,
		runID:   rs.RunID,
		routes:  map[string]Route{},
		targets: map[string]*url.URL{},
		ledger:  l,
		faults:  newFaultFile(cfg.FaultPath, cfg.Logger),
		client: &http.Client{Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
		}},
		logger: cfg.Logger,
		now:    time.Now,
		hold:   sleepCtx,
	}
	s.external = map[string]*externalRoute{}
	for _, r := range rs.Routes {
		if r.Protocol == ProtocolExternal {
			x, err := newExternalRoute(cfg, r)
			if err != nil {
				_ = s.Close()
				return nil, err
			}
			s.external[r.Service] = x
			continue
		}
		u, err := url.Parse(r.Upstream)
		if err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("route %s: %w", r.Service, err)
		}
		s.routes[r.Service], s.targets[r.Service] = r, u
	}
	return s, nil
}

// ListenAndServe serves until ctx ends, then shuts down and closes the
// ledger.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{Addr: s.cfg.Listen, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		_ = s.ledger.Close()
		return fmt.Errorf("tool proxy on %s: %w", s.cfg.Listen, err)
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serr := srv.Shutdown(shut)
	if err := s.Close(); err != nil {
		return err
	}
	if serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return fmt.Errorf("shut down tool proxy: %w", serr)
	}
	return nil
}

// Close closes the ledger and the record cassettes. Tests that use the
// server as a handler call it.
func (s *Server) Close() error {
	errs := []error{s.ledger.Close()}
	for _, x := range s.external {
		errs = append(errs, x.close())
	}
	return errors.Join(errs...)
}

// ServeHTTP implements http.Handler: /healthz and /s/<service>/<path>.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		if err := s.ledger.Healthy(); err != nil {
			http.Error(w, "ledger: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/x/"); ok {
		s.serveExternal(w, r, rest) // External APIs area: real HTTP APIs behind the allowlist (external.go)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/s/")
	if !ok {
		http.Error(w, "not found: services are under /s/<service id>/", http.StatusNotFound)
		return
	}
	service, path, _ := strings.Cut(rest, "/")
	path = "/" + path
	rt, ok := s.routes[service]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown service %q", service), http.StatusNotFound)
		return
	}
	switch {
	case rt.Protocol == ProtocolMCP && r.Method == http.MethodPost && path == rt.MCPPath:
		s.serveMCP(w, r, rt, path)
	case rt.Protocol == ProtocolHTTP:
		s.serveHTTPTool(w, r, rt, path)
	default:
		s.pass(w, r, rt, path, r.Body)
	}
}

// serveMCP handles one POST to an MCP endpoint: it records every tools/call
// and tools/list in the body (one message or a batch), answers calls an error
// or timeout fault takes with a tool error instead of forwarding them, and
// forwards the rest.
func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request, rt Route, path string) {
	body, err := readUpTo(r.Body, maxBody)
	if errors.Is(err, errTooLarge) {
		s.logger.Warn("mcp request over the parse cap; passed through unrecorded", "service", rt.Service)
		s.pass(w, r, rt, path, io.MultiReader(bytes.NewReader(body), r.Body))
		return
	}
	if err != nil {
		http.Error(w, "read request: "+err.Error(), http.StatusBadRequest)
		return
	}
	items, batch, ok := parseRPC(body)
	if !ok {
		s.logger.Debug("mcp request is not JSON-RPC; passed through unrecorded", "service", rt.Service)
		s.pass(w, r, rt, path, bytes.NewReader(body))
		return
	}
	x := s.newExchange(rt)
	var forward, synthetic []json.RawMessage
	var faulted []*pending
	hold := 0
	for _, it := range items {
		key := idKey(it.msg.ID)
		switch it.msg.Method {
		case methodToolsList:
			if key != "" {
				x.track(key, methodToolsList, it.msg.Params)
			}
		case methodToolsCall:
			name, args := readToolParams(it.msg.Params)
			p := x.track(key, name, args)
			ft, hit := s.faults.decide(rt.Service, name)
			switch {
			case hit && ft.Mode == FaultPoison:
				p.poison = &ft
			case hit && key != "":
				p.row.Fault, p.row.Nonce = ft.Mode, ft.Nonce
				faulted = append(faulted, p)
				if ft.Mode == FaultTimeout {
					hold = max(hold, holdSeconds(ft))
				}
				res := toolErrorResult(faultMessage(ft))
				synthetic = append(synthetic, responseWithResult(it.msg.ID, res))
				p.answer(res, true, 0, s.now())
				continue
			}
		}
		forward = append(forward, it.raw)
	}
	if hold > 0 {
		if err := s.hold(r.Context(), time.Duration(hold)*time.Second); err != nil {
			x.abandon("the agent hung up during an injected timeout", true)
			return
		}
		for _, p := range faulted {
			p.row.LatencyMS = s.now().Sub(p.row.Started).Milliseconds()
		}
	}
	if len(forward) == 0 {
		out := synthetic[0]
		if batch {
			out = encodeBatch(synthetic)
		}
		w.Header().Set("Content-Type", "application/json")
		_, werr := w.Write(out)
		x.commitAll(werr != nil)
		return
	}
	out := body
	if len(forward) != len(items) {
		out = encodeBatch(forward)
	}
	resp, err := s.client.Do(s.upstreamRequest(r, rt, path, out))
	if err != nil {
		msg := "tool server unreachable: " + transportError(err)
		x.unanswered(http.StatusBadGateway, []byte(msg))
		for _, p := range faulted {
			p.undelivered = true
		}
		http.Error(w, msg, http.StatusBadGateway)
		x.commitAll(false)
		return
	}
	defer resp.Body.Close()
	if isEventStream(resp.Header) {
		s.relaySSE(w, r, resp, x, synthetic)
		return
	}
	s.relayJSON(w, resp, x, synthetic)
}

// relayJSON relays a plain JSON answer, recording each response that answers
// a tracked call and rewriting poisoned results. Synthetic answers to faulted
// calls of a batch are added to the batch's answer.
func (s *Server) relayJSON(w http.ResponseWriter, resp *http.Response, x *exchange, synthetic []json.RawMessage) {
	raw, err := readUpTo(resp.Body, maxBody)
	if err != nil && !errors.Is(err, errTooLarge) {
		x.unanswered(resp.StatusCode, []byte("read tool server answer: "+err.Error()))
		copyHeaders(w.Header(), resp.Header, false)
		w.WriteHeader(resp.StatusCode)
		_, werr := w.Write(raw)
		x.commitAll(werr != nil)
		return
	}
	if errors.Is(err, errTooLarge) {
		x.unanswered(resp.StatusCode, []byte("answer over the parse cap; not recorded"))
		copyHeaders(w.Header(), resp.Header, true)
		w.WriteHeader(resp.StatusCode)
		_, werr := w.Write(raw)
		if werr == nil {
			_, werr = io.Copy(w, resp.Body)
		}
		x.commitAll(werr != nil)
		return
	}
	items, _, ok := parseRPC(raw)
	changed := false
	var all []json.RawMessage
	if ok {
		for _, it := range items {
			if nr, _, rewritten := x.answerMessage(it, resp.StatusCode); rewritten {
				it.raw, changed = nr, true
			}
			all = append(all, it.raw)
		}
	}
	status := resp.StatusCode
	out := raw
	switch {
	case len(synthetic) > 0 && ok:
		out = encodeBatch(append(all, synthetic...))
	case len(synthetic) > 0:
		out, status = encodeBatch(synthetic), http.StatusOK
	case changed && len(all) == 1 && bytes.TrimSpace(raw)[0] != '[':
		out = all[0]
	case changed:
		out = encodeBatch(all)
	}
	x.unanswered(resp.StatusCode, raw)
	rewritten := len(synthetic) > 0 || changed
	copyHeaders(w.Header(), resp.Header, rewritten)
	if len(synthetic) > 0 && !ok {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, werr := w.Write(out)
	x.commitAll(werr != nil)
}

// relaySSE relays a server-sent event stream event by event, flushing each,
// recording each response that answers a tracked call and rewriting poisoned
// results. Synthetic answers to faulted calls of a batch go first.
func (s *Server) relaySSE(w http.ResponseWriter, r *http.Request, resp *http.Response, x *exchange, synthetic []json.RawMessage) {
	flusher, _ := w.(http.Flusher)
	copyHeaders(w.Header(), resp.Header, true)
	w.WriteHeader(resp.StatusCode)
	var werr error
	write := func(b []byte) {
		if werr != nil {
			return
		}
		if _, err := w.Write(b); err != nil {
			werr = err
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	for _, m := range synthetic {
		write([]byte("event: message\ndata: " + string(m) + "\n\n"))
	}
	x.commitFaulted(werr != nil)
	rd := newSSEReader(resp.Body)
	for {
		ev, err := rd.next()
		if len(ev.lines) > 0 || len(ev.end) > 0 {
			done := x.answerEvent(&ev, resp.StatusCode)
			write(ev.bytes())
			for _, p := range done {
				p.undelivered = werr != nil
				x.commit(p)
			}
		}
		if err != nil {
			break
		}
	}
	gone := werr != nil || r.Context().Err() != nil
	x.unanswered(resp.StatusCode, []byte("the event stream ended before an answer"))
	x.commitAll(gone)
}

// serveHTTPTool handles one request to a plain HTTP tool server: the method
// and path are the tool, the query and body its arguments, the response body
// its result.
func (s *Server) serveHTTPTool(w http.ResponseWriter, r *http.Request, rt Route, path string) {
	started := s.now()
	row := schemas.ToolCall{Server: rt.Service, Tool: r.Method + " " + path, Protocol: ProtocolHTTP, Started: started}
	body, err := readUpTo(r.Body, maxBody)
	if errors.Is(err, errTooLarge) {
		s.logger.Warn("http tool request over the parse cap; arguments not recorded", "service", rt.Service)
		resp, perr := s.client.Do(s.upstreamStream(r, rt, path, io.MultiReader(bytes.NewReader(body), r.Body)))
		if perr != nil {
			row.IsError, row.Status = true, http.StatusBadGateway
			row.Result, row.ResultSHA256 = Capture([]byte("tool server unreachable: " + transportError(perr)))
			http.Error(w, row.Result, http.StatusBadGateway)
			s.append(&row)
			return
		}
		defer resp.Body.Close()
		s.relayHTTPTool(w, resp, &row, nil)
		return
	}
	if err != nil {
		http.Error(w, "read request: "+err.Error(), http.StatusBadRequest)
		return
	}
	args := body
	if r.URL.RawQuery != "" {
		args = []byte("?" + r.URL.RawQuery)
		if len(body) > 0 {
			args = append(append(args, '\n'), body...)
		}
	}
	row.Arguments, row.ArgsSHA256 = Capture(args)
	ft, hit := s.faults.decide(rt.Service, row.Tool)
	if hit && ft.Mode != FaultPoison {
		row.Fault, row.Nonce, row.IsError = ft.Mode, ft.Nonce, true
		status := http.StatusInternalServerError
		if ft.Mode == FaultTimeout {
			status = http.StatusGatewayTimeout
			if err := s.hold(r.Context(), time.Duration(holdSeconds(ft))*time.Second); err != nil {
				row.Undelivered = true
				row.LatencyMS = s.now().Sub(started).Milliseconds()
				s.append(&row)
				return
			}
		}
		out, _ := json.Marshal(map[string]string{"error": faultMessage(ft)})
		row.Result, row.ResultSHA256 = Capture(out)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, werr := w.Write(out)
		row.Undelivered = werr != nil
		row.LatencyMS = s.now().Sub(started).Milliseconds()
		s.append(&row)
		return
	}
	resp, err := s.client.Do(s.upstreamRequest(r, rt, path, body))
	if err != nil {
		row.IsError, row.Status = true, http.StatusBadGateway
		row.Result, row.ResultSHA256 = Capture([]byte("tool server unreachable: " + transportError(err)))
		http.Error(w, row.Result, http.StatusBadGateway)
		row.LatencyMS = s.now().Sub(started).Milliseconds()
		s.append(&row)
		return
	}
	defer resp.Body.Close()
	var poison *schemas.ToolFault
	if hit {
		poison = &ft
	}
	s.relayHTTPTool(w, resp, &row, poison)
}

// relayHTTPTool relays a plain HTTP tool server's answer and records it. An
// event stream or a body over the parse cap is streamed; its hash still
// covers every byte relayed.
func (s *Server) relayHTTPTool(w http.ResponseWriter, resp *http.Response, row *schemas.ToolCall, poison *schemas.ToolFault) {
	row.Status = resp.StatusCode
	row.IsError = resp.StatusCode >= 400
	defer func() {
		row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
		s.append(row)
	}()
	if isEventStream(resp.Header) {
		copyHeaders(w.Header(), resp.Header, true)
		w.WriteHeader(resp.StatusCode)
		h := sha256.New()
		_, err := io.Copy(flushWriter{w}, io.TeeReader(resp.Body, h))
		row.ResultSHA256 = hex.EncodeToString(h.Sum(nil))
		row.Undelivered = err != nil && !errors.Is(err, io.EOF)
		return
	}
	raw, err := readUpTo(resp.Body, maxBody)
	if errors.Is(err, errTooLarge) {
		copyHeaders(w.Header(), resp.Header, true)
		w.WriteHeader(resp.StatusCode)
		h := sha256.New()
		h.Write(raw)
		row.Result = clip(raw, MaxRecorded)
		_, werr := w.Write(raw)
		if werr == nil {
			_, werr = io.Copy(w, io.TeeReader(resp.Body, h))
		}
		row.ResultSHA256 = hex.EncodeToString(h.Sum(nil))
		row.Undelivered = werr != nil
		return
	}
	if err != nil {
		row.IsError = true
	}
	changed := false
	if poison != nil {
		raw = poisonHTTPBody(raw, resp.Header.Get("Content-Type"), poisonText(*poison))
		row.Fault, row.Nonce, changed = FaultPoison, poison.Nonce, true
	}
	row.Result, row.ResultSHA256 = Capture(raw)
	copyHeaders(w.Header(), resp.Header, changed)
	w.WriteHeader(resp.StatusCode)
	_, werr := w.Write(raw)
	row.Undelivered = werr != nil
}

// pass forwards a request unrecorded and streams the answer back: MCP GET
// streams, session DELETEs, and any path of an MCP service that is not its
// endpoint.
func (s *Server) pass(w http.ResponseWriter, r *http.Request, rt Route, path string, body io.Reader) {
	target := s.targets[rt.Service]
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = target.Scheme, target.Host
			pr.Out.URL.Path, pr.Out.URL.RawPath = strings.TrimRight(target.Path, "/")+path, ""
			pr.Out.Host = target.Host
		},
		Transport:     s.client.Transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "tool server unreachable: "+transportError(err), http.StatusBadGateway)
		},
	}
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(body)
	rp.ServeHTTP(w, r2)
}

// append writes a row to the ledger. A write failure is logged and shows on
// /healthz; the call itself was already answered.
func (s *Server) append(row *schemas.ToolCall) {
	if err := s.ledger.Append(row); err != nil {
		s.logger.Error("tool ledger write failed", "error", err.Error())
		return
	}
	s.logger.Debug("tool call", "seq", row.Seq, "server", row.Server, "tool", row.Tool, "is_error", row.IsError, "fault", row.Fault)
}

// upstreamRequest builds the request to the tool server with body.
func (s *Server) upstreamRequest(r *http.Request, rt Route, path string, body []byte) *http.Request {
	req := s.upstreamStream(r, rt, path, bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	return req
}

// upstreamStream builds the request to the tool server. Headers pass through
// (the MCP session id, protocol version and authorization among them) except
// hop-by-hop ones and Accept-Encoding, so the transport negotiates and
// undoes compression and the proxy always reads plain bodies.
func (s *Server) upstreamStream(r *http.Request, rt Route, path string, body io.Reader) *http.Request {
	u := *s.targets[rt.Service]
	u.Path, u.RawPath = strings.TrimRight(u.Path, "/")+path, ""
	u.RawQuery = r.URL.RawQuery
	req := (&http.Request{
		Method:        r.Method,
		URL:           &u,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{},
		Body:          io.NopCloser(body),
		ContentLength: -1,
		Host:          u.Host,
	}).WithContext(r.Context())
	copyHeaders(req.Header, r.Header, true)
	req.Header.Del("Accept-Encoding")
	return req
}

// newExchange starts the record of one request.
func (s *Server) newExchange(rt Route) *exchange {
	return &exchange{s: s, route: rt, started: s.now(), byKey: map[string]*pending{}}
}

// hopHeaders are never forwarded by a proxy (RFC 9110 7.6.1).
var hopHeaders = []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

// copyHeaders copies src into dst without hop-by-hop headers. dropLength
// also drops Content-Length, for a body the proxy rewrites or streams.
func copyHeaders(dst, src http.Header, dropLength bool) {
	for k, vs := range src {
		dst[k] = append([]string(nil), vs...)
	}
	for _, h := range hopHeaders {
		dst.Del(h)
	}
	if dropLength {
		dst.Del("Content-Length")
	}
}

// isEventStream reports whether a response is a server-sent event stream.
func isEventStream(h http.Header) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(h.Get("Content-Type"))), "text/event-stream")
}

var errTooLarge = errors.New("body over the parse cap")

// readUpTo reads all of r up to limit bytes. Over the limit it returns what
// it read (limit bytes) and errTooLarge; the caller streams the rest.
func readUpTo(r io.Reader, limit int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return b, err
	}
	if int64(len(b)) == limit {
		var one [1]byte
		n, _ := io.ReadFull(r, one[:])
		if n > 0 {
			return append(b, one[:n]...), errTooLarge
		}
	}
	return b, nil
}

// transportError describes a transport failure without the URL.
func transportError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// flushWriter flushes after every write, for streamed bodies.
type flushWriter struct{ w http.ResponseWriter }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

// poisonHTTPBody appends text to a plain HTTP tool answer: as a "notice"
// member of a JSON object (so the body stays valid JSON), else as trailing
// text.
func poisonHTTPBody(body []byte, contentType, text string) []byte {
	if strings.Contains(strings.ToLower(contentType), "json") {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(body, &obj); err == nil && obj != nil {
			notice := text
			var prev string
			if json.Unmarshal(obj["notice"], &prev) == nil && prev != "" {
				notice = prev + "\n\n" + text
			}
			b, _ := json.Marshal(notice)
			obj["notice"] = b
			if out, err := json.Marshal(obj); err == nil {
				return out
			}
		}
	}
	out := append([]byte(nil), body...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, text...)
}
