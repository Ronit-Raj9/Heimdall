// Package faultproxy is the bench's fault-injecting provider upstream. It sits
// behind Bifrost as the base URL of each proxied provider (DESIGN.md 13.3):
// agent, Bifrost, fault proxy, then the provider's API or the scripted model.
// A provider table (DefaultProviders, overridable by file) says, per provider,
// where the real API is, where its key goes, which paths are conversation
// calls and which error envelope it speaks. The proxy identifies the run's key
// slot from the placeholder credential Bifrost sends, applies that slot's
// fault plan to conversation requests, swaps in the real provider key, and
// writes every request to a hash-chained ledger outside the agent's reach.
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
	"os"
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

// Upstream modes, as /health reports them.
const (
	modeReal         = "real"
	modeScripted     = UpstreamScripted
	modeUnconfigured = "unconfigured"
)

// Config configures a Server.
//
// Each provider's real key is read once, at NewServer, from the environment
// variable its ProviderSpec names (through Getenv). A provider with no key
// answers from the scripted model when its format has one (anthropic,
// openai) and with a 503 "no key configured" otherwise. Upstream and
// UpstreamKey are the older single-provider form and apply to anthropic
// only: "scripted" forces its scripted model, a URL replaces its base URL and
// then needs UpstreamKey.
type Config struct {
	Listen        string // data plane, the address Bifrost's base_url points at
	ControlListen string // control API; loopback by default
	ControlToken  string // bearer token for control writes; generated when empty
	Upstream      string // anthropic only: "scripted" or a URL like https://api.anthropic.com
	UpstreamKey   string // anthropic only: real key for Upstream, never logged
	Providers     []ProviderSpec
	Getenv        func(string) string // os.Getenv when nil
	LedgerPath    string
	Slots         []SlotConfig
	Logger        schemas.Logger
}

// SlotConfig is one key slot of a provider in Bifrost. Bifrost key names are
// unique across providers, so Name alone identifies the slot.
type SlotConfig struct {
	Name     string // provider key name in Bifrost, e.g. fp-slot-3; the plan table key
	Auth     string // the placeholder key value Bifrost sends for this slot
	Provider string // provider name in the table; anthropic when empty
}

// Server is the fault proxy: a data plane for Bifrost and a control plane for
// the bench.
type Server struct {
	cfg       Config
	providers *providerTable
	upstreams map[string]upstream // provider name -> how it is answered
	slots     map[string]slotRef  // placeholder credential -> slot
	names     map[string]bool
	plans     *planTable
	ledger    *ledger
	client    *http.Client
	logger    schemas.Logger
	token     string
}

// upstream is how one provider is answered: the real API with a key, the
// scripted model, or not at all.
type upstream struct {
	mode string
	base string
	key  string
}

// slotRef is a slot and the provider it belongs to.
type slotRef struct {
	name     string
	provider string
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
	specs := cfg.Providers
	if len(specs) == 0 {
		specs = DefaultProviders()
	}
	table, err := indexProviders(specs)
	if err != nil {
		return nil, err
	}
	ups, err := resolveUpstreams(cfg, table)
	if err != nil {
		return nil, err
	}
	slots, names, err := indexSlots(withDefaultSlots(cfg.Slots, table), table)
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
		cfg: cfg, providers: table, upstreams: ups, slots: slots, names: names,
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

// serveData handles one request from Bifrost: identify the slot and its
// provider, classify, decide, answer, record.
func (s *Server) serveData(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w}
	slot, ok := s.slotOf(r)
	p := s.providers.guess(r)
	if ok {
		p = s.providers.byName[slot.provider]
	}
	kind := p.classify(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
	if err != nil {
		s.writeError(sw, p.Format, http.StatusBadRequest, errInvalid, "could not read request body")
		return
	}
	if len(body) > maxRequestBody {
		s.writeError(sw, p.Format, http.StatusRequestEntityTooLarge, errTooLarge, "request body too large")
		return
	}
	e := &schemas.LedgerEntry{Kind: kind, Path: r.URL.Path, Upstream: s.ledgerUpstream(p), ReqSHA256: utils.SHA256Bytes(body), At: start.UTC()}
	if !ok {
		s.writeError(sw, p.Format, http.StatusUnauthorized, errAuth, "invalid api key")
		e.Action, e.Status = ActionReject, sw.status()
		s.record(e)
		return
	}
	var req struct {
		Stream bool `json:"stream"`
	}
	if kind == KindMessage && json.Unmarshal(body, &req) != nil {
		req.Stream = false
	}
	if p.Format == FormatGemini && strings.Contains(r.URL.Path, ":streamGenerateContent") {
		req.Stream = true
	}
	d := s.plans.begin(slot.name, kind)
	e.Slot, e.RunID, e.Call, e.Stream = slot.name, d.RunID, d.Call, req.Stream
	switch {
	case d.Inject:
		s.writeFault(sw, p, d)
		e.Action, e.Nonce = ActionInject, d.Nonce
	case d.Scripted:
		e.Action, e.Upstream = ActionPass, schemas.LedgerUpstreamScripted
		upstreamStart := time.Now()
		s.answerScripted(sw, r, p, kind, slot.name, body, e)
		e.UpstreamMS = float64(time.Since(upstreamStart).Microseconds()) / 1000
	case d.Cut && req.Stream: // Safety probes area (S1): stream_cut
		s.answerCut(sw, r, p, kind, slot.name, body, e, d)
		return
	default:
		e.Action = ActionPass
		upstreamStart := time.Now()
		s.answer(sw, r, p, kind, slot.name, body, e)
		e.UpstreamMS = float64(time.Since(upstreamStart).Microseconds()) / 1000
	}
	e.Status = sw.status()
	s.record(e)
}

// answerCut answers a streamed conversation request the stream_cut fault
// takes: the answer (real upstream or scripted model) is relayed through a
// streamCutter. When the cutter cut the stream, the ledger line is an inject
// line carrying the plan's nonce, written before the connection is aborted,
// so the client sees the stream end without its final events. When the
// stream held no tool call it passed whole and the line is a pass line.
func (s *Server) answerCut(sw *statusWriter, r *http.Request, p ProviderSpec, kind, slot string, body []byte, e *schemas.LedgerEntry, d decision) {
	cutter := newStreamCutter(sw, p.Format)
	e.Action = ActionPass
	upstreamStart := time.Now()
	if d.Scripted {
		e.Upstream = schemas.LedgerUpstreamScripted
		s.answerScripted(cutter, r, p, kind, slot, body, e)
	} else {
		s.answer(cutter, r, p, kind, slot, body, e)
	}
	e.UpstreamMS = float64(time.Since(upstreamStart).Microseconds()) / 1000
	e.Status = sw.status()
	if !cutter.cut {
		cutter.finish()
		s.record(e)
		return
	}
	e.Action, e.Nonce, e.Tokens = ActionInject, d.Nonce, nil
	s.record(e)
	// Abort the response: net/http closes the connection without the final
	// chunk, so the client reads a truncated stream.
	panic(http.ErrAbortHandler)
}

// answer passes one request on: to the real upstream, the scripted model, or
// a 503 when the provider has neither.
func (s *Server) answer(w http.ResponseWriter, r *http.Request, p ProviderSpec, kind, slot string, body []byte, e *schemas.LedgerEntry) {
	up := s.upstreams[p.Name]
	switch {
	case up.mode == modeReal:
		s.forward(w, r, p, up, body, e)
	case up.mode == modeScripted && p.Format == FormatOpenAI:
		s.serveScriptedOpenAI(w, r, kind, slot, body, e)
	case up.mode == modeScripted:
		s.serveScripted(w, r, kind, slot, body, e)
	default:
		s.writeError(w, p.Format, http.StatusServiceUnavailable, errUpstream,
			fmt.Sprintf("fault proxy: no key configured for provider %s (set %s)", p.Name, p.KeyEnv))
	}
}

// ledgerUpstream is the ledger's upstream label for provider p: real when its
// requests go to the provider's API (with its key, or keyless like ollama),
// scripted when the scripted model answers them, empty when nothing does. It
// is the provider's route, not who wrote this one response, so injected and
// rejected lines carry it too and a run is labelled even if every call was
// faulted.
func (s *Server) ledgerUpstream(p ProviderSpec) string {
	switch s.upstreams[p.Name].mode {
	case modeReal:
		return schemas.LedgerUpstreamReal
	case modeScripted:
		return schemas.LedgerUpstreamScripted
	default:
		return ""
	}
}

// record appends e to the ledger and logs the decision without any secret.
func (s *Server) record(e *schemas.LedgerEntry) {
	if err := s.ledger.append(e); err != nil {
		s.logger.Error("fault proxy ledger append failed", "slot", e.Slot, "run_id", e.RunID, "error", err.Error())
	}
	s.logger.Debug("fault proxy request", "slot", e.Slot, "run_id", e.RunID, "kind", e.Kind, "call", e.Call,
		"action", e.Action, "status", e.Status, "response_id", e.ResponseID)
}

// serveScripted answers an Anthropic-format request from the scripted model.
func (s *Server) serveScripted(w http.ResponseWriter, r *http.Request, kind, slot string, body []byte, e *schemas.LedgerEntry) {
	w.Header().Set("request-id", utils.NewID("req", 24))
	switch kind {
	case KindMessage:
		var req messagesRequest
		if err := json.Unmarshal(body, &req); err != nil {
			req = messagesRequest{}
		}
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
		s.writeError(w, FormatAnthropic, http.StatusNotFound, errNotFound, "no scripted route for "+r.Method+" "+r.URL.Path)
	}
}

// forward relays any request to the provider's real upstream (same method,
// path and query) with the real key in the provider's auth style, then
// copies the response back chunk by chunk, flushing each one, while a bounded
// sniffer reads the response id and usage. Only safe headers are forwarded;
// every credential location the table knows is stripped first.
//
// When the provider gives no response at all, e.UpstreamError records why, so
// the engine can tell an unreachable provider from a provider error and never
// count it against the agent. A request whose caller went away first is not
// an upstream failure and is left unmarked.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, p ProviderSpec, up upstream, body []byte, e *schemas.LedgerEntry) {
	q := r.URL.Query()
	for _, a := range s.providers.auths {
		if a.Query != "" {
			q.Del(a.Query)
		}
	}
	target := up.base + r.URL.EscapedPath()
	out, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		e.UpstreamError = "build request: " + redactURLError(err)
		s.writeError(w, p.Format, http.StatusBadGateway, errUpstream, "fault proxy could not build the upstream request")
		return
	}
	out.URL.RawQuery = q.Encode()
	for _, name := range forwardedRequestHeaders {
		if v := r.Header.Values(name); len(v) > 0 {
			out.Header[http.CanonicalHeaderKey(name)] = v
		}
	}
	if up.key != "" {
		p.Auth.apply(out, up.key)
	}
	out.Header.Set("Accept-Encoding", "identity")
	resp, err := s.client.Do(out)
	if err != nil {
		msg := redactURLError(err)
		s.logger.Warn("upstream request failed", "slot", e.Slot, "provider", p.Name, "path", r.URL.Path, "error", msg)
		if r.Context().Err() == nil {
			e.UpstreamError = msg
		}
		s.writeError(w, p.Format, http.StatusBadGateway, errUpstream, utils.ProxyUnreachable)
		return
	}
	defer resp.Body.Close()
	for name, values := range resp.Header {
		if !droppedResponseHeaders[strings.ToLower(name)] {
			w.Header()[name] = values
		}
	}
	w.WriteHeader(resp.StatusCode)
	sn := newSniffer(p.Format, strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream"))
	if err := copyFlushing(w, io.TeeReader(resp.Body, sn)); err != nil {
		s.logger.Warn("upstream copy interrupted", "slot", e.Slot, "provider", p.Name, "path", r.URL.Path, "error", err.Error())
	}
	if e.Kind == KindMessage {
		e.ResponseID, e.Tokens = sn.result()
	}
}

// slotOf maps the credential Bifrost sent to a configured slot. Every auth
// style in the table is tried, so a slot is found wherever its placeholder
// sits; the slot then decides the provider.
func (s *Server) slotOf(r *http.Request) (slotRef, bool) {
	for _, a := range s.providers.auths {
		if v := a.credential(r); v != "" {
			if ref, ok := s.slots[v]; ok {
				return ref, true
			}
		}
	}
	return slotRef{}, false
}

// upstreamLabel names how each provider is answered, for /health and logs.
func (s *Server) upstreamLabel() map[string]string {
	out := make(map[string]string, len(s.upstreams))
	for name, up := range s.upstreams {
		out[name] = up.mode
		if up.mode == modeReal {
			out[name] = up.base
		}
	}
	return out
}

// writeJSON writes v as a JSON response. A failed write means the client went
// away; it is logged because nothing is left to tell the client.
func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		s.logger.Error("fault proxy encode response", "error", err.Error())
		http.Error(w, `{"error":{"message":"encode response"}}`, http.StatusInternalServerError)
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
var forwardedRequestHeaders = []string{"anthropic-version", "anthropic-beta", "openai-beta", "content-type", "accept"}

// droppedResponseHeaders are hop-by-hop, recomputed by net/http, or name the
// real account behind the key.
var droppedResponseHeaders = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true, "proxy-authorization": true,
	"te": true, "trailer": true, "transfer-encoding": true, "upgrade": true, "content-length": true,
	"set-cookie": true, "openai-organization": true, "openai-project": true, "anthropic-organization-id": true,
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

// resolveUpstreams decides how each provider is answered and reads its key.
// Keys are never logged.
func resolveUpstreams(cfg Config, t *providerTable) (map[string]upstream, error) {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	out := make(map[string]upstream, len(t.order))
	for _, name := range t.order {
		p := t.byName[name]
		up := upstream{base: p.BaseURL}
		if p.KeyEnv != "" {
			up.key = strings.TrimSpace(getenv(p.KeyEnv))
		}
		switch {
		case up.key != "" || p.Keyless:
			up.mode = modeReal
		case p.scriptable():
			up.mode = modeScripted
		default:
			up.mode = modeUnconfigured
		}
		out[name] = up
	}
	if cfg.Upstream == "" {
		return out, nil
	}
	if _, ok := t.byName[ProviderAnthropic]; !ok {
		return nil, errors.New("upstream is set but the provider table has no anthropic entry")
	}
	if cfg.Upstream == UpstreamScripted {
		out[ProviderAnthropic] = upstream{mode: modeScripted, base: t.byName[ProviderAnthropic].BaseURL}
		return out, nil
	}
	base, err := parseBaseURL(cfg.Upstream)
	if err != nil {
		return nil, fmt.Errorf("upstream: %w", err)
	}
	if cfg.UpstreamKey == "" {
		return nil, errors.New("real upstream needs an upstream key")
	}
	out[ProviderAnthropic] = upstream{mode: modeReal, base: base, key: cfg.UpstreamKey}
	return out, nil
}

// withDefaultSlots adds <prefix>-slot-1..4 (auth = name) for every provider
// cfg lists no slot for. Slots without a provider are anthropic's.
func withDefaultSlots(cfg []SlotConfig, t *providerTable) []SlotConfig {
	out := make([]SlotConfig, 0, len(cfg)+defaultSlotsPerProvider*len(t.order))
	has := map[string]bool{}
	for _, sc := range cfg {
		if sc.Provider == "" {
			sc.Provider = ProviderAnthropic
		}
		has[sc.Provider] = true
		out = append(out, sc)
	}
	for _, name := range t.order {
		if has[name] {
			continue
		}
		for _, sc := range DefaultSlots(t.byName[name]) {
			out = append(out, sc)
		}
	}
	return out
}

// DefaultSlots returns the slots a provider gets when none are configured:
// <slot_prefix>-slot-1..4, each with its name as the placeholder value.
func DefaultSlots(p ProviderSpec) []SlotConfig {
	out := make([]SlotConfig, 0, defaultSlotsPerProvider)
	for i := 1; i <= defaultSlotsPerProvider; i++ {
		name := fmt.Sprintf("%s-slot-%d", p.slotPrefix(), i)
		out = append(out, SlotConfig{Name: name, Auth: name, Provider: p.Name})
	}
	return out
}

func indexSlots(cfg []SlotConfig, t *providerTable) (map[string]slotRef, map[string]bool, error) {
	if len(cfg) == 0 {
		return nil, nil, errors.New("no key slots configured")
	}
	byAuth := make(map[string]slotRef, len(cfg))
	names := make(map[string]bool, len(cfg))
	for i, sc := range cfg {
		if sc.Name == "" || sc.Auth == "" {
			return nil, nil, fmt.Errorf("slot %d needs a name and an auth value", i+1)
		}
		if _, ok := t.byName[sc.Provider]; !ok {
			return nil, nil, fmt.Errorf("slot %q names unknown provider %q", sc.Name, sc.Provider)
		}
		if names[sc.Name] {
			return nil, nil, fmt.Errorf("slot %q configured twice", sc.Name)
		}
		if _, dup := byAuth[sc.Auth]; dup {
			return nil, nil, fmt.Errorf("slot %q reuses another slot's auth value", sc.Name)
		}
		byAuth[sc.Auth] = slotRef{name: sc.Name, provider: sc.Provider}
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
