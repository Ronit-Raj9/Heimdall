// Package faultproxy is the bench's fault-injecting Anthropic upstream. It sits
// behind Bifrost as the base URL of the built-in anthropic provider (DESIGN.md
// 13.3): agent, Bifrost, fault proxy, then the Anthropic API or the scripted
// model. It identifies the run's key slot from the placeholder credential
// Bifrost sends, applies that slot's fault plan to conversation requests,
// swaps in the real provider key, and writes every request to a hash-chained
// ledger outside the agent's reach.
//
// It uses net/http rather than fasthttp because it streams; that is the one
// stated exception in bench/AGENTS.md.
package faultproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Defaults for Config.
const (
	DefaultListen        = ":9100"
	DefaultControlListen = "127.0.0.1:9101"
	UpstreamScripted     = "scripted"

	maxRequestBody  = 32 << 20
	maxCapturedBody = 8 << 20
	copyBufferSize  = 32 << 10
	shutdownTimeout = 10 * time.Second
)

// Config configures a Server.
type Config struct {
	Listen        string // data plane, the address Bifrost's base_url points at
	ControlListen string // control API; loopback by default
	ControlToken  string // bearer token for control writes; generated when empty
	Upstream      string // "scripted" or a URL like https://api.anthropic.com
	UpstreamKey   string // real provider key, from env, never logged
	LedgerPath    string
	Slots         []SlotConfig
	Logger        schemas.Logger
}

// SlotConfig is one key slot of the anthropic provider in Bifrost.
type SlotConfig struct {
	Name string // provider key name in Bifrost, e.g. fp-slot-3; the plan table key
	Auth string // the placeholder key value Bifrost sends for this slot
}

// Server is the fault proxy: a data plane for Bifrost and a control plane for
// the bench.
type Server struct {
	cfg      Config
	upstream string // "" in scripted mode, else the base URL without a trailing slash
	slots    map[string]string
	names    map[string]bool
	plans    *planTable
	ledger   *ledger
	client   *http.Client
	logger   schemas.Logger
	token    string
}

// NewServer validates cfg, opens the ledger and returns a Server. The caller
// must Close it.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Listen == "" {
		cfg.Listen = DefaultListen
	}
	if cfg.ControlListen == "" {
		cfg.ControlListen = DefaultControlListen
	}
	logger := cfg.Logger
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	upstream, err := parseUpstream(cfg.Upstream)
	if err != nil {
		return nil, err
	}
	if upstream != "" && cfg.UpstreamKey == "" {
		return nil, errors.New("real upstream needs an upstream key")
	}
	slots, names, err := indexSlots(cfg.Slots)
	if err != nil {
		return nil, err
	}
	token := cfg.ControlToken
	if token == "" {
		if token, err = randomHex(24); err != nil {
			return nil, fmt.Errorf("generate control token: %w", err)
		}
	}
	lg, err := openLedger(cfg.LedgerPath)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg: cfg, upstream: upstream, slots: slots, names: names,
		plans: newPlanTable(), ledger: lg, client: newUpstreamClient(),
		logger: logger, token: token,
	}, nil
}

// ControlToken returns the bearer token control writes need. Hand it to the
// bench's Client; never to the agent.
func (s *Server) ControlToken() string {
	return s.token
}

// Handler returns the data plane handler, the one Bifrost talks to.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveData)
}

// ListenAndServe binds the data and control listeners and serves until ctx is
// done, then shuts both down gracefully.
func (s *Server) ListenAndServe(ctx context.Context) error {
	dataLn, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen data plane %s: %w", s.cfg.Listen, err)
	}
	ctrlLn, err := net.Listen("tcp", s.cfg.ControlListen)
	if err != nil {
		if cerr := dataLn.Close(); cerr != nil {
			s.logger.Warn("close data listener", "error", cerr.Error())
		}
		return fmt.Errorf("listen control plane %s: %w", s.cfg.ControlListen, err)
	}
	return s.Serve(ctx, dataLn, ctrlLn)
}

// Serve serves the data plane on dataLn and the control API on ctrlLn until
// ctx is done or either server fails. Streams get no write timeout because a
// long model response is legitimate.
func (s *Server) Serve(ctx context.Context, dataLn, ctrlLn net.Listener) error {
	data := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 30 * time.Second}
	ctrl := &http.Server{Handler: s.ControlHandler(), ReadHeaderTimeout: 10 * time.Second, WriteTimeout: time.Minute}
	errc := make(chan error, 2)
	go func() { errc <- serveOne(data, dataLn) }()
	go func() { errc <- serveOne(ctrl, ctrlLn) }()
	s.logger.Info("fault proxy listening", "data", dataLn.Addr().String(), "control", ctrlLn.Addr().String(),
		"upstream", s.upstreamLabel(), "slots", len(s.slots))
	var first error
	select {
	case <-ctx.Done():
	case first = <-errc:
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, srv := range []*http.Server{data, ctrl} {
		if err := srv.Shutdown(sctx); err != nil && first == nil {
			first = fmt.Errorf("shutdown: %w", err)
		}
	}
	return first
}

// Close closes the ledger.
func (s *Server) Close() error {
	return s.ledger.close()
}

// serveData handles one request from Bifrost: identify the slot, classify,
// decide, answer, record.
func (s *Server) serveData(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w}
	kind := classify(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
	if err != nil {
		s.writeError(sw, http.StatusBadRequest, "invalid_request_error", "could not read request body", 0)
		return
	}
	if len(body) > maxRequestBody {
		s.writeError(sw, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large", 0)
		return
	}
	e := &schemas.LedgerEntry{Kind: kind, Path: r.URL.Path, ReqSHA256: utils.SHA256Bytes(body), At: start.UTC()}
	slot, ok := s.slotOf(r)
	if !ok {
		s.writeError(sw, http.StatusUnauthorized, "authentication_error", "invalid x-api-key", 0)
		e.Action, e.Status = ActionReject, sw.status()
		s.record(e)
		return
	}
	var req messagesRequest
	if kind == KindMessage {
		if err := json.Unmarshal(body, &req); err != nil {
			req = messagesRequest{}
		}
	}
	d := s.plans.begin(slot, kind)
	e.Slot, e.RunID, e.Call, e.Stream = slot, d.RunID, d.Call, req.Stream
	if d.Inject {
		s.writeFault(sw, d)
		e.Action, e.Nonce = ActionInject, d.Nonce
	} else {
		e.Action = ActionPass
		upstreamStart := time.Now()
		if s.upstream != "" {
			s.forward(sw, r, body, e)
		} else {
			s.serveScripted(sw, r, kind, slot, body, req, e)
		}
		e.UpstreamMS = float64(time.Since(upstreamStart).Microseconds()) / 1000
	}
	e.Status = sw.status()
	s.record(e)
}

// record appends e to the ledger and logs the decision without any secret.
func (s *Server) record(e *schemas.LedgerEntry) {
	if err := s.ledger.append(e); err != nil {
		s.logger.Error("fault proxy ledger append failed", "slot", e.Slot, "run_id", e.RunID, "error", err.Error())
	}
	s.logger.Debug("fault proxy request", "slot", e.Slot, "run_id", e.RunID, "kind", e.Kind, "call", e.Call,
		"action", e.Action, "status", e.Status, "response_id", e.ResponseID)
}

// serveScripted answers from the scripted model.
func (s *Server) serveScripted(w http.ResponseWriter, r *http.Request, kind, slot string, body []byte, req messagesRequest, e *schemas.LedgerEntry) {
	w.Header().Set("request-id", utils.NewID("req", 24))
	switch kind {
	case KindMessage:
		rp := newScriptedReply(body, req, len(req.Tools) > 0 && s.plans.takeToolTurn(slot))
		e.ResponseID, e.Tokens = rp.ID, rp.tokens()
		if req.Stream {
			if err := writeScriptedStream(w, rp); err != nil {
				s.logger.Warn("scripted stream interrupted", "slot", slot, "error", err.Error())
			}
			return
		}
		s.writeJSON(w, http.StatusOK, rp.message())
	case KindCountTokens:
		s.writeJSON(w, http.StatusOK, map[string]any{"input_tokens": scriptedInputTokens(body)})
	case KindModels:
		if id := strings.TrimPrefix(r.URL.Path, "/v1/models/"); id != r.URL.Path && id != "" {
			s.writeJSON(w, http.StatusOK, scriptedModel(id, id))
			return
		}
		s.writeJSON(w, http.StatusOK, scriptedModelList())
	default:
		s.writeError(w, http.StatusNotFound, "not_found_error", "no scripted route for "+r.Method+" "+r.URL.Path, 0)
	}
}

// forward relays the request to the real upstream with the real key, then
// copies the response back chunk by chunk, flushing each one, while a
// bounded sniffer reads the message id and usage.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, body []byte, e *schemas.LedgerEntry) {
	target := s.upstream + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	out, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		s.writeError(w, http.StatusBadGateway, "api_error", "fault proxy could not build the upstream request", 0)
		return
	}
	for _, name := range forwardedRequestHeaders {
		if v := r.Header.Values(name); len(v) > 0 {
			out.Header[http.CanonicalHeaderKey(name)] = v
		}
	}
	out.Header.Set("x-api-key", s.cfg.UpstreamKey)
	out.Header.Set("Accept-Encoding", "identity")
	resp, err := s.client.Do(out)
	if err != nil {
		s.logger.Warn("upstream request failed", "slot", e.Slot, "path", r.URL.Path, "error", redactURLError(err))
		s.writeError(w, http.StatusBadGateway, "api_error", "fault proxy could not reach the upstream", 0)
		return
	}
	defer resp.Body.Close()
	for name, values := range resp.Header {
		if !droppedResponseHeaders[strings.ToLower(name)] {
			w.Header()[name] = values
		}
	}
	w.WriteHeader(resp.StatusCode)
	sn := newSniffer(strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream"))
	if err := copyFlushing(w, io.TeeReader(resp.Body, sn)); err != nil {
		s.logger.Warn("upstream copy interrupted", "slot", e.Slot, "path", r.URL.Path, "error", err.Error())
	}
	if e.Kind == KindMessage {
		e.ResponseID, e.Tokens = sn.result()
	}
}

// slotOf maps the credential Bifrost sent to a configured slot.
func (s *Server) slotOf(r *http.Request) (string, bool) {
	v := r.Header.Get("x-api-key")
	if v == "" {
		if a := r.Header.Get("Authorization"); len(a) > 7 && strings.EqualFold(a[:7], "bearer ") {
			v = strings.TrimSpace(a[7:])
		}
	}
	if v == "" {
		return "", false
	}
	name, ok := s.slots[v]
	return name, ok
}

func (s *Server) upstreamLabel() string {
	if s.upstream == "" {
		return UpstreamScripted
	}
	return s.upstream
}

// classify names a request's kind. Only POST /v1/messages is a conversation
// request; everything else passes uncounted.
func classify(r *http.Request) string {
	p := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodPost && p == "/v1/messages":
		return KindMessage
	case r.Method == http.MethodPost && p == "/v1/messages/count_tokens":
		return KindCountTokens
	case r.Method == http.MethodGet && (p == "/v1/models" || strings.HasPrefix(p, "/v1/models/")):
		return KindModels
	default:
		return KindOther
	}
}

// writeFault writes Anthropic's error envelope for an injected failure. The
// run's nonce rides in the message: the only place the agent can ever see it.
// The call number after it makes each injected message unique, so a Bifrost
// error row can be joined to exactly one ledger entry.
func (s *Server) writeFault(w http.ResponseWriter, d decision) {
	errType, msg := "api_error", "Internal server error."
	if d.Status == http.StatusServiceUnavailable || d.Status == 529 {
		errType, msg = "overloaded_error", "Overloaded: the model is temporarily unavailable."
	}
	if d.Nonce != "" {
		msg += " ref " + d.Nonce + " call " + strconv.Itoa(d.Call)
	}
	s.writeError(w, d.Status, errType, msg, d.RetryAfterS)
}

// writeError writes Anthropic's error envelope,
// {"type":"error","error":{"type":...,"message":...},"request_id":...}.
func (s *Server) writeError(w http.ResponseWriter, status int, errType, msg string, retryAfterS int) {
	reqID := utils.NewID("req", 24)
	w.Header().Set("request-id", reqID)
	if retryAfterS > 0 {
		w.Header().Set("retry-after", strconv.Itoa(retryAfterS))
	}
	s.writeJSON(w, status, map[string]any{
		"type":       "error",
		"error":      map[string]string{"type": errType, "message": msg},
		"request_id": reqID,
	})
}

// writeJSON writes v as a JSON response. A failed write means the client went
// away; it is logged because nothing is left to tell the client.
func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		s.logger.Error("fault proxy encode response", "error", err.Error())
		http.Error(w, `{"type":"error","error":{"type":"api_error","message":"encode response"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(data); err != nil {
		s.logger.Debug("fault proxy write response", "error", err.Error())
	}
}

// copyFlushing copies src to w, flushing after every read so SSE events reach
// Bifrost as soon as the upstream sends them.
func copyFlushing(w http.ResponseWriter, src io.Reader) error {
	rc := http.NewResponseController(w)
	buf := make([]byte, copyBufferSize)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write to client: %w", werr)
			}
			if ferr := rc.Flush(); ferr != nil {
				return fmt.Errorf("flush to client: %w", ferr)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read upstream: %w", err)
		}
	}
}

// forwardedRequestHeaders are the only request headers sent upstream; the
// credential is replaced, never copied.
var forwardedRequestHeaders = []string{"anthropic-version", "anthropic-beta", "content-type", "accept"}

// droppedResponseHeaders are hop-by-hop or recomputed by net/http.
var droppedResponseHeaders = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true, "proxy-authorization": true,
	"te": true, "trailer": true, "transfer-encoding": true, "upgrade": true, "content-length": true,
	"set-cookie": true,
}

func newUpstreamClient() *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   16,
		DisableCompression:    true,
	}
	return &http.Client{Transport: tr}
}

func parseUpstream(raw string) (string, error) {
	if raw == "" || raw == UpstreamScripted {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse upstream %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("upstream must be %q or an http(s) URL, got %q", UpstreamScripted, raw)
	}
	if u.User != nil || u.RawQuery != "" {
		return "", errors.New("upstream URL must not carry credentials or a query")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func indexSlots(cfg []SlotConfig) (map[string]string, map[string]bool, error) {
	if len(cfg) == 0 {
		return nil, nil, errors.New("no key slots configured")
	}
	byAuth := make(map[string]string, len(cfg))
	names := make(map[string]bool, len(cfg))
	for i, sc := range cfg {
		if sc.Name == "" || sc.Auth == "" {
			return nil, nil, fmt.Errorf("slot %d needs a name and an auth value", i+1)
		}
		if names[sc.Name] {
			return nil, nil, fmt.Errorf("slot %q configured twice", sc.Name)
		}
		if _, dup := byAuth[sc.Auth]; dup {
			return nil, nil, fmt.Errorf("slot %q reuses another slot's auth value", sc.Name)
		}
		byAuth[sc.Auth] = sc.Name
		names[sc.Name] = true
	}
	return byAuth, names, nil
}

// redactURLError keeps the error kind but drops the URL, which is all a
// transport error could leak.
func redactURLError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + ": " + ue.Err.Error()
	}
	return err.Error()
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func serveOne(srv *http.Server, ln net.Listener) error {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve %s: %w", ln.Addr(), err)
	}
	return nil
}

// statusWriter remembers the status code. Unwrap lets
// http.ResponseController reach the underlying Flusher.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusWriter) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}
