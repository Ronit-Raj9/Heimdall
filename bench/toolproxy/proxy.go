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
// on traffic it does not understand and lets nothing through unrecorded:
// every request gets a ledger row. A body that is not JSON-RPC, is over the
// parse cap, or is of a content type or encoding the proxy does not read is
// relayed unchanged and recorded as an opaque row whose hash and byte count
// cover every byte (opaque.go), and a call whose answer never arrives is
// recorded as failed.
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
	"net/url"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxBody caps a request or response body of a plain HTTP tool server the
// proxy reads into memory to record. A larger body is passed through as a
// stream. MCP traffic uses Server.parseCap (defaultParseCap, opaque.go).
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

	// parseCap is the most kept in memory to parse one MCP message
	// (defaultParseCap; tests lower it).
	parseCap int64

	// external holds the external routes (external.go); they are never in
	// routes, so /s/<id> cannot reach them.
	external map[string]*externalRoute
	// members maps team member route tokens to member ids (members.go).
	members map[string]string
	// frames saves the screenshots agents get, for the screen replay (frames.go).
	frames *frameRecorder
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
		frames:  newFrameRecorder(cfg.LedgerPath),
		faults:  newFaultFile(cfg.FaultPath, cfg.Logger),
		client: &http.Client{Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
		}},
		logger:   cfg.Logger,
		now:      time.Now,
		hold:     sleepCtx,
		parseCap: defaultParseCap,
	}
	s.external = map[string]*externalRoute{}
	s.members = rs.Members // Multi-agent area: team member route tokens (members.go)
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
	if s.serveCollectorPath(w, r) { // Agent events area: POST /events and /otlp/v1/traces (collector.go)
		return
	}
	// The call is in flight until its row is written: the answer reaches
	// the agent first, so the ledger status says when a copy would miss it.
	defer s.ledger.Begin()()
	agent, ok := s.memberRoute(w, r) // Multi-agent area: /m/<token>/ names the team member (members.go)
	if !ok {
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/x/"); ok {
		s.serveExternal(w, r, rest, agent) // External APIs area: real HTTP APIs behind the allowlist (external.go)
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
	rt.Agent = agent
	switch {
	case rt.Protocol == ProtocolMCP && r.Method == http.MethodPost && path == rt.MCPPath:
		s.serveMCP(w, r, rt, path)
	case rt.Protocol == ProtocolHTTP:
		s.serveHTTPTool(w, r, rt, path)
	default:
		s.pass(w, r, rt, path)
	}
}

// serveMCP handles one POST to an MCP endpoint: it records every tools/call
// and tools/list in the body (one message or a batch), answers calls an error
// or timeout fault takes with a tool error instead of forwarding them, and
// forwards the rest.
func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request, rt Route, path string) {
	// Every byte of the request goes through req, so an opaque row's hash
	// and count cover the whole body however much of it was kept.
	req := newMeter()
	in := io.TeeReader(r.Body, req)
	var body []byte
	var err error
	if r.ContentLength <= s.parseCap {
		body, err = readUpTo(in, s.parseCap)
	} else {
		err = errTooLarge
	}
	if errors.Is(err, errTooLarge) {
		s.logger.Warn("mcp request over the parse cap; relayed unparsed", "service", rt.Service)
		s.forwardOpaque(w, r, rt, path, io.MultiReader(bytes.NewReader(body), in), req, fmt.Sprintf("request body over the %d MiB parse cap", s.parseCap>>20))
		return
	}
	if err != nil {
		row := s.baseRow(rt, r, path, schemas.ToolRowOpaque, schemas.ToolRowUnparsedTool)
		row.Unparsed, row.UnparsedReason = true, "reading the request failed: "+err.Error()
		http.Error(w, "read request: "+err.Error(), http.StatusBadRequest)
		s.finishRow(&row, req, relayed{status: http.StatusBadRequest, sha: emptySHA})
		return
	}
	text, decoded := body, false
	if enc := contentEncoding(r.Header); enc != "" {
		d, derr := decodeBody(body, enc, s.parseCap)
		if derr != nil {
			s.forwardOpaque(w, r, rt, path, bytes.NewReader(body), req, "request content encoding "+enc+": "+derr.Error())
			return
		}
		text, decoded = d, true
	}
	if mt := notJSONType(r.Header.Get("Content-Type")); mt != "" {
		s.forwardOpaque(w, r, rt, path, bytes.NewReader(body), req, "request content type "+mt+" is not JSON")
		return
	}
	items, batch, ok := parseRPC(text)
	if !ok || len(items) == 0 {
		reason := "request body is not JSON-RPC"
		if ok {
			reason = "request body is an empty JSON-RPC batch"
		}
		s.forwardOpaque(w, r, rt, path, bytes.NewReader(body), req, reason)
		return
	}
	x := s.newExchange(rt)
	var forward, synthetic []json.RawMessage
	var faulted []*pending
	hold := 0
	for _, it := range items {
		key := idKey(it.msg.ID)
		switch m := it.msg; {
		case m.Method != methodToolsCall:
			// Everything else the client sends gets a row of its own kind,
			// so the handshake, listings, resources, prompts, notifications
			// and answers to server requests are visible; none is a tool call.
			p := s.clientMessage(x, it, key)
			p.row.RequestBytes = int64(len(it.raw))
			if m.Method == methodToolsList && key != "" {
				if ft, ok := s.faults.listing(rt.Service, rt.Agent); ok { // Safety probes area (S1): MCP tool poisoning
					p.listing = &ft
				}
			}
		default:
			name, args := readToolParams(it.msg.Params)
			p := x.track(key, name, args)
			p.row.RequestBytes = int64(len(it.raw))
			ft, hit := s.faults.decide(rt.Service, name, rt.Agent)
			switch {
			case hit && (ft.Mode == FaultPoison || ft.Mode == FaultInflate): // inflate appends filler the way poison appends text
				p.poison = &ft
			case hit && IsQuietMode(ft.Mode): // Safety probes area (S1)
				p.quiet = &ft
			case hit && ft.Mode == FaultLostAck && key != "": // Safety probes area (S1)
				p.lostAck = &ft
			case hit && ft.Mode == FaultLostAck:
				// A notification gets no answer to withhold: forwarded as is.
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
	upReq := s.upstreamRequest(r, rt, path, out)
	if decoded && len(forward) != len(items) {
		upReq.Header.Del("Content-Encoding") // the rebuilt batch is sent plain
	}
	resp, err := s.client.Do(upReq)
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
	if x.lostAckHold() > 0 { // Safety probes area (S1): lost_ack
		s.relayLostAck(w, r, resp, x, synthetic, batch)
		return
	}
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
	raw, err := readUpTo(resp.Body, s.parseCap)
	if err != nil && !errors.Is(err, errTooLarge) {
		x.unanswered(resp.StatusCode, []byte("read tool server answer: "+err.Error()))
		copyHeaders(w.Header(), resp.Header, false)
		w.WriteHeader(resp.StatusCode)
		_, werr := w.Write(raw)
		x.commitFaulted(len(synthetic) > 0) // the synthetic answers were not sent
		x.commitAll(werr != nil)
		return
	}
	if errors.Is(err, errTooLarge) {
		// Relayed whole and streamed; the hash and count cover every byte.
		// Synthetic answers to faulted calls of the batch cannot be merged
		// into an answer the proxy does not parse, so the agent never sees
		// them: those calls are recorded as undelivered, not as faults the
		// agent witnessed.
		copyHeaders(w.Header(), resp.Header, true)
		w.WriteHeader(resp.StatusCode)
		m := newMeter()
		m.Write(raw)
		_, werr := w.Write(raw)
		if werr == nil {
			_, werr = io.Copy(w, io.TeeReader(resp.Body, m))
		}
		sha, n := m.sum()
		x.unparsedAnswer(resp.StatusCode, fmt.Sprintf("answer over the %d MiB parse cap; relayed whole, not parsed", s.parseCap>>20), sha, n)
		x.commitFaulted(len(synthetic) > 0)
		x.commitAll(werr != nil)
		return
	}
	text := raw
	if enc := contentEncoding(resp.Header); enc != "" {
		d, derr := decodeBody(raw, enc, s.parseCap)
		if derr != nil {
			x.unparsedAnswer(resp.StatusCode, "answer content encoding "+enc+": "+derr.Error(), sha256Hex(raw), int64(len(raw)))
			copyHeaders(w.Header(), resp.Header, false)
			w.WriteHeader(resp.StatusCode)
			_, werr := w.Write(raw)
			x.commitFaulted(len(synthetic) > 0) // the synthetic answers were not sent
			x.commitAll(werr != nil)
			return
		}
		text = d
	}
	items, _, ok := parseRPC(text)
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
	case changed && len(all) == 1 && bytes.TrimSpace(text)[0] != '[':
		out = all[0]
	case changed:
		out = encodeBatch(all)
	}
	x.unanswered(resp.StatusCode, text)
	rewritten := len(synthetic) > 0 || changed
	copyHeaders(w.Header(), resp.Header, rewritten)
	if rewritten {
		w.Header().Del("Content-Encoding") // a rewritten answer is sent plain
	}
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
	var src io.Reader = resp.Body
	undecodable := ""
	if enc := contentEncoding(resp.Header); enc != "" {
		// A compressed stream is decoded to be parsed and relayed decoded.
		if d, err := decodeReader(resp.Body, enc); err == nil {
			src = d
			w.Header().Del("Content-Encoding")
		} else {
			undecodable = "answer stream content encoding " + enc + ": " + err.Error()
		}
	}
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
	if undecodable != "" {
		// The stream is passed through under its own coding, so a plain
		// synthetic event cannot be mixed into it: the faulted calls'
		// answers are omitted and their rows say undelivered.
		x.commitFaulted(len(synthetic) > 0)
		m := newMeter()
		_, _ = io.Copy(writerFunc(func(b []byte) { m.Write(b); write(b) }), src)
		sha, n := m.sum()
		x.unparsedAnswer(resp.StatusCode, undecodable, sha, n)
		x.commitAll(werr != nil || r.Context().Err() != nil)
		return
	}
	for _, m := range synthetic {
		write([]byte("event: message\ndata: " + string(m) + "\n\n"))
	}
	x.commitFaulted(werr != nil)
	rd := newSSEReader(src, int(s.parseCap))
	var bigSHA string
	var bigN int64
	for {
		ev, big, err := rd.next()
		if big {
			// One event over the parse cap: relayed whole as it streams,
			// hashed and counted, recorded as an opaque row.
			m := newMeter()
			b := ev.bytes()
			m.Write(b)
			write(b)
			rerr := rd.rest(func(p []byte) { m.Write(p); write(p) })
			bigSHA, bigN = m.sum()
			s.oversizedEvent(x.route, resp, bigSHA, bigN)
			if rerr != nil {
				break
			}
			continue
		}
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
	if bigN > 0 {
		x.unparsedAnswer(resp.StatusCode, fmt.Sprintf("an event of the answer stream was over the %d MiB parse cap; relayed whole, not parsed", s.parseCap>>20), bigSHA, bigN)
	}
	x.unanswered(resp.StatusCode, []byte("the event stream ended before an answer"))
	x.commitAll(gone)
}

// writerFunc adapts a function to io.Writer.
type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) { f(p); return len(p), nil }

// oversizedEvent records one event of a server's stream that was over the
// parse cap as an opaque row.
func (s *Server) oversizedEvent(rt Route, resp *http.Response, sha string, n int64) {
	row := schemas.ToolCall{
		Server: rt.Service, Tool: schemas.ToolRowUnparsedTool, Protocol: rt.Protocol, Kind: schemas.ToolRowOpaque,
		Started: s.now(), Status: resp.StatusCode, ResponseContentType: resp.Header.Get("Content-Type"),
		ArgsSHA256: emptySHA, ResultSHA256: sha, ResponseBytes: n, Unparsed: true,
		UnparsedReason: fmt.Sprintf("event stream event over the %d MiB parse cap; relayed whole, not parsed", s.parseCap>>20),
	}
	if resp.Request != nil {
		row.HTTPMethod, row.Path = resp.Request.Method, resp.Request.URL.Path
	}
	s.append(&row)
}

// clientMessage starts the row of a message the client sent that is not a
// tools/call: a request of another method, a notification, an answer to a
// server request, or something that is not a JSON-RPC message at all.
func (s *Server) clientMessage(x *exchange, it rpcItem, key string) *pending {
	m := it.msg
	switch {
	case m.Method != "" && key != "":
		return x.trackAs(schemas.ToolRowMCPMethod, key, m.Method, m.Params)
	case m.Method != "":
		return x.trackAs(schemas.ToolRowNotification, "", m.Method, m.Params)
	case key != "" || len(m.Result) > 0 || len(m.Error) > 0:
		return x.trackAs(schemas.ToolRowResponse, "", "response", it.raw)
	}
	p := x.trackAs(schemas.ToolRowOpaque, "", schemas.ToolRowUnparsedTool, it.raw)
	p.row.Unparsed, p.row.UnparsedReason = true, "batch element is not a JSON-RPC message"
	return p
}

// serverMessage records a message the server sent that answers no call the
// proxy tracked: a request to the client (sampling, elicitation, roots), a
// notification (progress, log messages, list changes), or an answer to an id
// it does not know. It is written at once.
func (s *Server) serverMessage(rt Route, it rpcItem, status int) {
	m := it.msg
	row := schemas.ToolCall{Server: rt.Service, Protocol: rt.Protocol, Started: s.now(), Status: status, ResponseBytes: int64(len(it.raw))}
	switch {
	case m.Method != "" && idKey(m.ID) != "":
		row.Kind, row.Tool = schemas.ToolRowServerRequest, m.Method
		row.Arguments, row.ArgsSHA256 = Capture(m.Params)
		row.ResultSHA256 = emptySHA
	case m.Method != "":
		row.Kind, row.Tool = schemas.ToolRowNotification, m.Method
		row.Arguments, row.ArgsSHA256 = Capture(m.Params)
		row.ResultSHA256 = emptySHA
	case idKey(m.ID) != "" || len(m.Result) > 0 || len(m.Error) > 0:
		row.Kind, row.Tool = schemas.ToolRowResponse, "response"
		row.ArgsSHA256 = emptySHA
		row.Result, row.ResultSHA256 = Capture(it.raw)
		row.IsError = len(bytes.TrimSpace(m.Error)) > 0 && string(bytes.TrimSpace(m.Error)) != "null"
	default:
		row.Kind, row.Tool = schemas.ToolRowOpaque, schemas.ToolRowUnparsedTool
		row.ArgsSHA256 = emptySHA
		row.Result, row.ResultSHA256 = Capture(it.raw)
		row.Unparsed, row.UnparsedReason = true, "server message is not a JSON-RPC message"
	}
	s.append(&row)
}

// serveHTTPTool handles one request to a plain HTTP tool server: the method
// and path are the tool, the query and body its arguments, the response body
// its result.
func (s *Server) serveHTTPTool(w http.ResponseWriter, r *http.Request, rt Route, path string) {
	started := s.now()
	row := schemas.ToolCall{Server: rt.Service, Tool: r.Method + " " + path, Protocol: ProtocolHTTP, Started: started, AgentID: rt.Agent}
	req := newMeter()
	in := io.TeeReader(r.Body, req)
	body, err := readUpTo(in, maxBody)
	if errors.Is(err, errTooLarge) {
		// Still a tool call; its arguments are not kept, but the hash and
		// count cover the whole request body, streamed.
		s.logger.Warn("http tool request over the parse cap; arguments not recorded", "service", rt.Service)
		row.Unparsed, row.UnparsedReason = true, fmt.Sprintf("request body over the %d MiB cap; arguments not kept", maxBody>>20)
		sb := newSignalBody(io.MultiReader(bytes.NewReader(body), in))
		resp, perr := s.client.Do(s.upstreamStream(r, rt, path, sb))
		if perr != nil {
			sb.wait(bodyWait)
			row.ArgsSHA256, row.RequestBytes = req.sum()
			row.IsError, row.Status = true, http.StatusBadGateway
			row.Result, row.ResultSHA256 = Capture([]byte("tool server unreachable: " + transportError(perr)))
			http.Error(w, row.Result, http.StatusBadGateway)
			s.append(&row)
			return
		}
		defer resp.Body.Close()
		s.relayHTTPTool(w, resp, &row, nil, func() { sb.wait(bodyWait); row.ArgsSHA256, row.RequestBytes = req.sum() })
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
	row.RequestBytes = int64(len(body))
	ft, hit := s.faults.decide(rt.Service, row.Tool, rt.Agent)
	if hit && (ft.Mode == FaultError || ft.Mode == FaultTimeout) {
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
	if hit && ft.Mode == FaultLostAck { // Safety probes area (S1)
		s.lostAckHTTP(w, r, resp, &row, ft)
		return
	}
	var poison *schemas.ToolFault
	if hit {
		poison = &ft
	}
	s.relayHTTPTool(w, resp, &row, poison, nil)
}

// relayHTTPTool relays a plain HTTP tool server's answer and records it. An
// event stream or a body over the parse cap is streamed; its hash still
// covers every byte relayed.
func (s *Server) relayHTTPTool(w http.ResponseWriter, resp *http.Response, row *schemas.ToolCall, poison *schemas.ToolFault, reqDone func()) {
	row.Status = resp.StatusCode
	row.IsError = resp.StatusCode >= 400
	row.ResponseContentType = resp.Header.Get("Content-Type")
	defer func() {
		if reqDone != nil {
			reqDone()
		}
		row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
		s.append(row)
	}()
	if isEventStream(resp.Header) {
		copyHeaders(w.Header(), resp.Header, true)
		w.WriteHeader(resp.StatusCode)
		h := sha256.New()
		n, err := io.Copy(flushWriter{w}, io.TeeReader(resp.Body, h))
		row.ResultSHA256, row.ResponseBytes = hex.EncodeToString(h.Sum(nil)), n
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
		var n int64
		if werr == nil {
			n, werr = io.Copy(w, io.TeeReader(resp.Body, h))
		}
		row.ResultSHA256, row.ResponseBytes = hex.EncodeToString(h.Sum(nil)), int64(len(raw))+n
		row.Unparsed, row.UnparsedReason = true, fmt.Sprintf("answer over the %d MiB cap; relayed whole, result cut", maxBody>>20)
		row.Undelivered = werr != nil
		return
	}
	if err != nil {
		row.IsError = true
	}
	changed := false
	if poison != nil {
		raw, changed = rewriteHTTPAnswer(raw, resp.Header.Get("Content-Type"), *poison)
		if changed {
			row.Fault, row.Nonce = poison.Mode, poison.Nonce
		}
	}
	row.Result, row.ResultSHA256 = Capture(raw)
	row.ResponseBytes = int64(len(raw))
	copyHeaders(w.Header(), resp.Header, changed)
	w.WriteHeader(resp.StatusCode)
	_, werr := w.Write(raw)
	row.Undelivered = werr != nil
}

// pass forwards a request unchanged and streams the answer back: MCP GET
// streams, session DELETEs, and any path of an MCP service that is not its
// endpoint. It writes one row: transport for a request without a body, opaque
// for one with a body (it was not parsed). The messages of a server's event
// stream are parsed one event at a time as they are relayed, and each gets a
// row of its own.
func (s *Server) pass(w http.ResponseWriter, r *http.Request, rt Route, path string) {
	req := newMeter()
	var body io.Reader
	if r.ContentLength != 0 && r.Body != nil && r.Body != http.NoBody {
		body = io.TeeReader(r.Body, req)
	}
	row := s.baseRow(rt, r, path, schemas.ToolRowTransport, r.Method+" "+path)
	notParsed, compressed := 0, false
	res := s.relayRecorded(w, r, rt, path, body, func(resp *http.Response) io.Writer {
		h, status := resp.Header, resp.StatusCode
		if !isEventStream(h) {
			return nil
		}
		if contentEncoding(h) != "" {
			compressed = true
			return nil
		}
		return &sseSplitter{
			limit: int(s.parseCap),
			onEvent: func(b []byte) {
				ev := eventOf(b)
				data, ok := ev.data()
				if !ok {
					return // a comment or keep-alive
				}
				items, _, ok := parseRPC(data)
				if !ok {
					notParsed++
					return
				}
				for _, it := range items {
					s.serverMessage(rt, it, status)
				}
			},
			onOver: func(sha string, n int64) {
				notParsed++
				s.oversizedEvent(rt, resp, sha, n)
			},
		}
	})
	if _, n := req.sum(); n > 0 {
		row.Kind, row.Tool = schemas.ToolRowOpaque, schemas.ToolRowUnparsedTool
		row.Unparsed, row.UnparsedReason = true, fmt.Sprintf("%s %s is not a POST to the MCP endpoint; body relayed unparsed", r.Method, path)
	}
	switch {
	case compressed:
		row.Unparsed, row.UnparsedReason = true, "compressed event stream relayed unparsed"
	case notParsed > 0 && !row.Unparsed:
		row.Unparsed, row.UnparsedReason = true, fmt.Sprintf("%d events of the stream carried no parsable JSON-RPC message", notParsed)
	}
	s.finishRow(&row, req, res)
}

// append writes a row to the ledger. A write failure is logged, shows on
// /healthz for the rest of the run and marks the ledger status incomplete;
// the call itself was already answered.
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
		Body:          readCloser(body),
		ContentLength: -1,
		Host:          u.Host,
	}).WithContext(r.Context())
	copyHeaders(req.Header, r.Header, true)
	req.Header.Del("Accept-Encoding")
	return req
}

// readCloser keeps a body's own Close (a signalBody says when the transport
// is done with it) and adds a no-op one otherwise.
func readCloser(r io.Reader) io.ReadCloser {
	if rc, ok := r.(io.ReadCloser); ok {
		return rc
	}
	return io.NopCloser(r)
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

// rewriteHTTPAnswer applies a poison or quiet fault to a plain HTTP or
// external answer. ok is false when the fault changed nothing.
func rewriteHTTPAnswer(body []byte, contentType string, ft schemas.ToolFault) ([]byte, bool) {
	switch {
	case ft.Mode == FaultPoison || ft.Mode == FaultInflate:
		return poisonHTTPBody(body, contentType, poisonText(ft)), true
	case IsQuietMode(ft.Mode): // Safety probes area (S1)
		return quietHTTPBody(body, ft)
	}
	return body, false
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
