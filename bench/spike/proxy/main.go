// Command faultproxy is the spike's fault-injecting Anthropic upstream. Bifrost's
// built-in anthropic provider points its base_url here. The proxy reads the run's key
// slot from the upstream credential Bifrost sends, applies that slot's fault plan to
// conversation requests (POST /v1/messages) only, and either answers itself
// (scripted mode, no API key) or forwards to api.anthropic.com with the real key
// (real mode). Every received request becomes one JSON line in the ledger.
//
// It uses net/http rather than fasthttp because it streams; that is the one stated
// exception in bench/AGENTS.md. Throwaway spike code: what works graduates into
// bench/faultproxy.
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxBody = 32 << 20

// plan is one slot's fault plan. Calls are numbered from 1 per slot, counted from the
// moment the plan was installed, and only POST /v1/messages counts.
type plan struct {
	Mode     string `json:"mode"`
	FromCall int    `json:"from_call,omitempty"`
	Call     int    `json:"call,omitempty"`
	Status   int    `json:"status"`
}

// ledgerEntry is one line of the ledger: one received request.
type ledgerEntry struct {
	TS          string            `json:"ts"`
	Slot        string            `json:"slot"`
	Call        int               `json:"call"`
	Counted     bool              `json:"counted"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Action      string            `json:"action"`
	Status      int               `json:"status"`
	Stream      bool              `json:"stream"`
	Model       string            `json:"model,omitempty"`
	ReqSHA256   string            `json:"req_sha256"`
	ResponseID  string            `json:"response_id,omitempty"`
	RequestID   string            `json:"request_id,omitempty"`
	Nonce       string            `json:"nonce,omitempty"`
	Upstream    string            `json:"upstream"`
	HeaderNames []string          `json:"header_names"`
	Headers     map[string]string `json:"headers"`
	DurationMS  int64             `json:"duration_ms"`
}

type server struct {
	mode         string
	realURL      string
	realKey      string
	controlToken string
	client       *http.Client

	mu    sync.Mutex
	plans map[string]plan
	calls map[string]int

	ledgerMu sync.Mutex
	ledger   *os.File
}

func main() {
	s := &server{
		mode:         envOr("FP_MODE", "scripted"),
		realURL:      strings.TrimRight(envOr("FP_REAL_URL", "https://api.anthropic.com"), "/"),
		realKey:      os.Getenv("ANTHROPIC_API_KEY"),
		controlToken: os.Getenv("FP_CONTROL_TOKEN"),
		client:       &http.Client{Timeout: 10 * time.Minute},
		plans:        map[string]plan{},
		calls:        map[string]int{},
	}
	if s.mode != "scripted" && s.mode != "real" {
		log.Fatalf("FP_MODE must be scripted or real, got %q", s.mode)
	}
	f, err := os.OpenFile(envOr("FP_LEDGER", "/ledger/ledger.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("open ledger: %v", err)
	}
	s.ledger = f
	go func() {
		log.Printf("control api on %s", envOr("FP_CONTROL", ":9101"))
		log.Fatal(http.ListenAndServe(envOr("FP_CONTROL", ":9101"), s.controlMux()))
	}()
	log.Printf("upstream api on %s mode=%s", envOr("FP_LISTEN", ":9100"), s.mode)
	log.Fatal(http.ListenAndServe(envOr("FP_LISTEN", ":9100"), http.HandlerFunc(s.serveUpstream)))
}

// serveUpstream handles every request Bifrost sends to the provider base URL.
func (s *server) serveUpstream(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "read body: "+err.Error(), "")
		return
	}
	sum := sha256.Sum256(body)
	var req messagesRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			req = messagesRequest{}
		}
	}
	slot := slotOf(r)
	e := &ledgerEntry{
		TS: start.UTC().Format(time.RFC3339Nano), Slot: slot, Method: r.Method, Path: r.URL.Path,
		ReqSHA256: hex.EncodeToString(sum[:]), Stream: req.Stream, Model: req.Model, Upstream: s.mode,
	}
	e.HeaderNames, e.Headers = describeHeaders(r.Header)
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	e.Action = "pass"
	var fault *plan
	if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
		e.Counted = true
		e.Call = s.nextCall(slot)
		if p, ok := s.planFor(slot); ok && p.fires(e.Call) {
			fault = &p
		}
	}
	if fault != nil {
		e.Action = "inject"
		e.Nonce = randomToken(16)
		e.RequestID = "req_" + randomToken(24)
		writeAnthropicError(rec, fault.Status, errorTypeFor(fault.Status),
			fmt.Sprintf("faultproxy injected %d on call %d (nonce %s)", fault.Status, e.Call, e.Nonce), e.RequestID)
	} else {
		s.dispatch(rec, r, body, &req, e)
	}
	e.Status = rec.status
	e.DurationMS = time.Since(start).Milliseconds()
	s.appendLedger(e)
	log.Printf("slot=%s call=%d %s %s action=%s status=%d id=%s headers=%v", e.Slot, e.Call, e.Method, e.Path, e.Action, e.Status, e.ResponseID, e.HeaderNames)
}

// dispatch answers a request that no fault plan intercepted.
func (s *server) dispatch(w http.ResponseWriter, r *http.Request, body []byte, req *messagesRequest, e *ledgerEntry) {
	if s.mode == "real" {
		s.forward(w, r, body, e)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		e.RequestID = "req_" + randomToken(24)
		e.ResponseID = "msg_" + randomToken(24)
		w.Header().Set("request-id", e.RequestID)
		reply := scriptedReply(req, e)
		if req.Stream {
			writeStream(w, reply)
			return
		}
		writeJSON(w, http.StatusOK, reply.message())
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages/count_tokens":
		writeJSON(w, http.StatusOK, map[string]int{"input_tokens": estimateTokens(len(body))})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		writeJSON(w, http.StatusOK, map[string]any{
			"data": scriptedModels(), "has_more": false,
			"first_id": scriptedModels()[0]["id"], "last_id": scriptedModels()[len(scriptedModels())-1]["id"],
		})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/models/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		for _, m := range scriptedModels() {
			if m["id"] == id {
				writeJSON(w, http.StatusOK, m)
				return
			}
		}
		writeAnthropicError(w, http.StatusNotFound, "not_found_error", "model not found: "+id, "")
	default:
		writeAnthropicError(w, http.StatusNotFound, "not_found_error", "faultproxy has no route "+r.Method+" "+r.URL.Path, "")
	}
}

// forward relays a request to the real Anthropic API, swapping the slot placeholder for
// the real key, and streams the response back while sniffing the message id.
func (s *server) forward(w http.ResponseWriter, r *http.Request, body []byte, e *ledgerEntry) {
	if s.realKey == "" {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "faultproxy real mode has no ANTHROPIC_API_KEY", "")
		return
	}
	out, err := http.NewRequestWithContext(r.Context(), r.Method, s.realURL+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "build upstream request: "+err.Error(), "")
		return
	}
	for name, values := range r.Header {
		switch strings.ToLower(name) {
		case "x-api-key", "authorization", "host", "content-length", "connection", "accept-encoding", "te", "upgrade", "proxy-authorization":
			continue
		}
		out.Header[name] = values
	}
	out.Header.Set("x-api-key", s.realKey)
	resp, err := s.client.Do(out)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "upstream: "+err.Error(), "")
		return
	}
	defer resp.Body.Close()
	for name, values := range resp.Header {
		switch strings.ToLower(name) {
		case "content-length", "connection", "transfer-encoding", "content-encoding":
			continue
		}
		w.Header()[name] = values
	}
	e.RequestID = resp.Header.Get("request-id")
	w.WriteHeader(resp.StatusCode)
	sniff := &prefixBuffer{limit: 64 << 10}
	copyFlushing(w, io.TeeReader(resp.Body, sniff))
	e.ResponseID = firstMessageID(sniff.buf.Bytes())
}

// controlMux is the loopback control API: fault plans per slot.
func (s *server) controlMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "mode": s.mode})
	})
	mux.HandleFunc("GET /plans", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"plans": s.plans, "calls": s.calls})
	})
	mux.HandleFunc("PUT /plans/{slot}", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(w, r) {
			return
		}
		var p plan
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&p); err != nil {
			http.Error(w, "decode plan: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := p.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.plans[r.PathValue("slot")] = p
		s.calls[r.PathValue("slot")] = 0
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"slot": r.PathValue("slot"), "plan": p, "calls_reset": true})
	})
	mux.HandleFunc("DELETE /plans/{slot}", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(w, r) {
			return
		}
		s.mu.Lock()
		delete(s.plans, r.PathValue("slot"))
		s.calls[r.PathValue("slot")] = 0
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (s *server) authorized(w http.ResponseWriter, r *http.Request) bool {
	if s.controlToken == "" || r.Header.Get("Authorization") == "Bearer "+s.controlToken {
		return true
	}
	http.Error(w, "control token required", http.StatusUnauthorized)
	return false
}

func (s *server) nextCall(slot string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[slot]++
	return s.calls[slot]
}

func (s *server) planFor(slot string) (plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.plans[slot]
	return p, ok
}

func (s *server) appendLedger(e *ledgerEntry) {
	line, err := json.Marshal(e)
	if err != nil {
		log.Printf("marshal ledger entry: %v", err)
		return
	}
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if _, err := s.ledger.Write(append(line, '\n')); err != nil {
		log.Printf("write ledger: %v", err)
	}
}

func (p plan) validate() error {
	if p.Status < 400 || p.Status > 599 {
		return fmt.Errorf("status must be 4xx or 5xx, got %d", p.Status)
	}
	switch p.Mode {
	case "persistent":
		if p.FromCall < 1 {
			return errors.New("persistent plan needs from_call >= 1")
		}
	case "once":
		if p.Call < 1 {
			return errors.New("once plan needs call >= 1")
		}
	default:
		return fmt.Errorf("mode must be persistent or once, got %q", p.Mode)
	}
	return nil
}

func (p plan) fires(call int) bool {
	if p.Mode == "persistent" {
		return call >= p.FromCall
	}
	return call == p.Call
}

// slotOf reads the run's key slot from the upstream credential. Slot values are
// placeholders, never real keys.
func slotOf(r *http.Request) string {
	v := r.Header.Get("x-api-key")
	if v == "" {
		v = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	if v == "" {
		return "none"
	}
	if !slotPattern.MatchString(v) {
		return "invalid"
	}
	return v
}

var slotPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// describeHeaders lists every header name Bifrost sent and keeps values only for
// non-secret metadata, so the ledger can show what crosses the wire without leaking keys.
func describeHeaders(h http.Header) ([]string, map[string]string) {
	names := make([]string, 0, len(h))
	out := make(map[string]string, len(h))
	for name, values := range h {
		lower := strings.ToLower(name)
		names = append(names, lower)
		value := strings.Join(values, ", ")
		switch {
		case lower == "x-api-key" || lower == "authorization":
			out[lower] = "[slot credential redacted]"
		case strings.Contains(lower, "key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
			strings.Contains(lower, "auth") || strings.Contains(lower, "cookie") || strings.Contains(lower, "signature"):
			out[lower] = "[redacted]"
		case nonSecretHeaders[lower] || strings.HasSuffix(lower, "-id") || strings.HasPrefix(lower, "x-stainless-"):
			out[lower] = value
		default:
			out[lower] = fmt.Sprintf("[redacted len=%d]", len(value))
		}
	}
	sort.Strings(names)
	return names, out
}

var nonSecretHeaders = map[string]bool{
	"anthropic-version": true, "anthropic-beta": true, "content-type": true, "content-length": true,
	"accept": true, "accept-encoding": true, "user-agent": true, "connection": true,
}

// messagesRequest is the subset of an Anthropic Messages request the proxy reads.
type messagesRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Name string `json:"name"`
	} `json:"tools"`
}

// reply is a scripted assistant turn, rendered either as JSON or as SSE events.
type reply struct {
	ID, Model, Text, ToolID, ToolName, StopReason string
	ToolInput                                     map[string]any
	Input, Output, CacheRead, CacheWrite          int
}

func scriptedReply(req *messagesRequest, e *ledgerEntry) reply {
	model := req.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	rp := reply{
		ID: e.ResponseID, Model: model, StopReason: "end_turn",
		Text:  fmt.Sprintf("Scripted reply %d for slot %s.", e.Call, e.Slot),
		Input: 8, CacheWrite: 32, CacheRead: 16 * e.Call,
	}
	if raw, err := json.Marshal(req.Messages); err == nil {
		rp.Input = estimateTokens(len(raw))
	}
	lastIsToolResult := false
	if n := len(req.Messages); n > 0 {
		lastIsToolResult = bytes.Contains(req.Messages[n-1].Content, []byte(`"tool_result"`))
	}
	if len(req.Tools) > 0 && !lastIsToolResult {
		rp.ToolID = "toolu_" + randomToken(24)
		rp.ToolName = req.Tools[0].Name
		rp.ToolInput = map[string]any{"note": "scripted tool call"}
		rp.StopReason = "tool_use"
	}
	rp.Output = estimateTokens(len(rp.Text)) + 2
	return rp
}

func (rp reply) usage() map[string]int {
	return map[string]int{
		"input_tokens": rp.Input, "output_tokens": rp.Output,
		"cache_read_input_tokens": rp.CacheRead, "cache_creation_input_tokens": rp.CacheWrite,
	}
}

func (rp reply) content() []map[string]any {
	blocks := []map[string]any{{"type": "text", "text": rp.Text}}
	if rp.ToolID != "" {
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": rp.ToolID, "name": rp.ToolName, "input": rp.ToolInput})
	}
	return blocks
}

func (rp reply) message() map[string]any {
	return map[string]any{
		"id": rp.ID, "type": "message", "role": "assistant", "model": rp.Model,
		"content": rp.content(), "stop_reason": rp.StopReason, "stop_sequence": nil, "usage": rp.usage(),
	}
}

// writeStream renders the reply as Anthropic SSE events.
func writeStream(w http.ResponseWriter, rp reply) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	startUsage := rp.usage()
	startUsage["output_tokens"] = 1
	events := []map[string]any{{"type": "message_start", "message": map[string]any{
		"id": rp.ID, "type": "message", "role": "assistant", "model": rp.Model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": startUsage,
	}}}
	events = append(events,
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "ping"})
	half := len(rp.Text) / 2
	for _, part := range []string{rp.Text[:half], rp.Text[half:]} {
		events = append(events, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": part}})
	}
	events = append(events, map[string]any{"type": "content_block_stop", "index": 0})
	if rp.ToolID != "" {
		input, err := json.Marshal(rp.ToolInput)
		if err != nil {
			input = []byte("{}")
		}
		events = append(events,
			map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "tool_use", "id": rp.ToolID, "name": rp.ToolName, "input": map[string]any{}}},
			map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}},
			map[string]any{"type": "content_block_stop", "index": 1})
	}
	events = append(events,
		map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": rp.StopReason, "stop_sequence": nil}, "usage": rp.usage()},
		map[string]any{"type": "message_stop"})
	flusher, _ := w.(http.Flusher)
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], data)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func scriptedModels() []map[string]any {
	return []map[string]any{
		{"type": "model", "id": "claude-sonnet-4-6", "display_name": "Claude Sonnet 4.6", "created_at": "2026-02-17T00:00:00Z"},
		{"type": "model", "id": "claude-haiku-5-5", "display_name": "Claude Haiku 5.5", "created_at": "2026-09-01T00:00:00Z"},
	}
}

// writeAnthropicError writes Anthropic's error envelope.
func writeAnthropicError(w http.ResponseWriter, status int, errType, msg, requestID string) {
	body := map[string]any{"type": "error", "error": map[string]string{"type": errType, "message": msg}}
	if requestID != "" {
		body["request_id"] = requestID
		w.Header().Set("request-id", requestID)
	}
	writeJSON(w, status, body)
}

func errorTypeFor(status int) string {
	switch status {
	case 400:
		return "invalid_request_error"
	case 401:
		return "authentication_error"
	case 403:
		return "permission_error"
	case 404:
		return "not_found_error"
	case 413:
		return "request_too_large"
	case 429:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	}
	return "api_error"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "marshal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(data); err != nil {
		log.Printf("write response: %v", err)
	}
}

func copyFlushing(w http.ResponseWriter, r io.Reader) {
	flusher, _ := w.(http.Flusher)
	br := bufio.NewReaderSize(r, 32<<10)
	buf := make([]byte, 32<<10)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

var messageIDPattern = regexp.MustCompile(`"id"\s*:\s*"(msg_[A-Za-z0-9]+)"`)

func firstMessageID(b []byte) string {
	if m := messageIDPattern.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// prefixBuffer keeps the first limit bytes written to it.
type prefixBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (p *prefixBuffer) Write(b []byte) (int, error) {
	if room := p.limit - p.buf.Len(); room > 0 {
		if len(b) < room {
			room = len(b)
		}
		p.buf.Write(b[:room])
	}
	return len(b), nil
}

// statusRecorder remembers the status code and passes Flush through.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func estimateTokens(n int) int {
	if n < 4 {
		return 1
	}
	return n / 4
}

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randomToken(n int) string {
	var b strings.Builder
	max := big.NewInt(int64(len(alphabet)))
	for range n {
		i, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(fmt.Sprintf("crypto/rand: %v", err))
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String()
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
