package faultproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// testEnv is one proxy with its data and control planes on httptest servers.
type testEnv struct {
	srv    *Server
	data   *httptest.Server
	ctrl   *httptest.Server
	client *Client
	ledger string
}

func newTestEnv(t *testing.T, upstream, key string) *testEnv {
	t.Helper()
	return newTestEnvConfig(t, Config{Upstream: upstream, UpstreamKey: key})
}

// newTestEnvConfig starts a proxy from cfg with slots slot-a and slot-b
// (anthropic) and oa-1 (openai) unless cfg names slots, and an empty
// environment unless cfg sets Getenv, so a developer's real keys never leak
// into a test.
func newTestEnvConfig(t *testing.T, cfg Config) *testEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	cfg.LedgerPath = path
	if cfg.Slots == nil {
		cfg.Slots = []SlotConfig{{Name: "slot-a", Auth: "auth-a"}, {Name: "slot-b", Auth: "auth-b"},
			{Name: "oa-1", Auth: "oa-auth-1", Provider: ProviderOpenAI}}
	}
	if cfg.Getenv == nil {
		cfg.Getenv = func(string) string { return "" }
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	env := &testEnv{srv: srv, data: httptest.NewServer(srv.Handler()), ctrl: httptest.NewServer(srv.ControlHandler()), ledger: path}
	env.client = NewClient(env.ctrl.URL, WithToken(srv.ControlToken()))
	t.Cleanup(func() {
		env.data.Close()
		env.ctrl.Close()
		if err := srv.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	return env
}

// call sends one request to the data plane with the slot credential in
// x-api-key and returns the response with its body read.
func (env *testEnv) call(t *testing.T, method, path, auth, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, env.data.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if auth != "" {
		req.Header.Set("x-api-key", auth)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, data
}

func (env *testEnv) entries(t *testing.T) []schemas.LedgerEntry {
	t.Helper()
	f, err := os.Open(env.ledger)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer f.Close()
	all, err := ReadLedger(f)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if err := VerifyChain(all); err != nil {
		t.Fatalf("ledger chain: %v", err)
	}
	return all
}

func (env *testEnv) setPlan(t *testing.T, slot string, p schemas.FaultPlan) {
	t.Helper()
	if err := env.client.SetPlan(context.Background(), slot, p); err != nil {
		t.Fatalf("SetPlan: %v", err)
	}
}

const msgBody = `{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`

func TestSlotIdentification(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	cases := []struct {
		name     string
		header   string
		value    string
		status   int
		wantSlot string
	}{
		{"x-api-key slot a", "x-api-key", "auth-a", 200, "slot-a"},
		{"bearer slot b", "Authorization", "Bearer auth-b", 200, "slot-b"},
		{"unknown key", "x-api-key", "sk-ant-real-looking", 401, ""},
		{"slot name is not a credential", "x-api-key", "slot-a", 401, ""},
		{"no credential", "", "", 401, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, env.data.URL+"/v1/messages", strings.NewReader(msgBody))
			if err != nil {
				t.Fatal(err)
			}
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			entries := env.entries(t)
			last := entries[len(entries)-1]
			if last.Slot != tc.wantSlot {
				t.Errorf("ledger slot %q, want %q", last.Slot, tc.wantSlot)
			}
			if tc.status == 401 {
				e := decodeError(t, body)
				if e.Error.Type != "authentication_error" {
					t.Errorf("error type %q, want authentication_error", e.Error.Type)
				}
				if last.Action != ActionReject || last.RunID != "" || last.Call != 0 {
					t.Errorf("rejected entry = %+v", last)
				}
				if strings.Contains(string(body), tc.value) && tc.value != "" {
					t.Errorf("401 body echoes the credential")
				}
			}
		})
	}
}

func TestCountingExcludesUncountedKinds(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeNone, RunID: "r1"})
	steps := []struct {
		method, path string
		kind         string
		call         int
	}{
		{http.MethodGet, "/v1/models", KindModels, 0},
		{http.MethodPost, "/v1/messages/count_tokens", KindCountTokens, 0},
		{http.MethodPost, "/v1/messages", KindMessage, 1},
		{http.MethodGet, "/v1/models/claude-sonnet-4-6", KindModels, 0},
		{http.MethodPost, "/v1/messages", KindMessage, 2},
		{http.MethodPost, "/v1/complete", KindOther, 0},
		{http.MethodGet, "/v1/messages", KindOther, 0},
		{http.MethodPost, "/v1/messages", KindMessage, 3},
	}
	for _, st := range steps {
		env.call(t, st.method, st.path, "auth-a", msgBody)
	}
	got, err := env.client.Ledger(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(steps) {
		t.Fatalf("ledger has %d entries for r1, want %d", len(got), len(steps))
	}
	for i, st := range steps {
		if got[i].Kind != st.kind || got[i].Call != st.call {
			t.Errorf("entry %d %s %s: kind %s call %d, want %s %d", i, st.method, st.path, got[i].Kind, got[i].Call, st.kind, st.call)
		}
	}
}

func TestPlans(t *testing.T) {
	cases := []struct {
		name string
		plan schemas.FaultPlan
		want []int // status per conversation call
	}{
		{"none", schemas.FaultPlan{Mode: ModeNone}, []int{200, 200, 200, 200}},
		{"persistent from 2", schemas.FaultPlan{Mode: ModePersistent, FromCall: 2, Status: 503}, []int{200, 503, 503, 503}},
		{"persistent from 1", schemas.FaultPlan{Mode: ModePersistent, FromCall: 1, Status: 529}, []int{529, 529, 529, 529}},
		{"once at 2", schemas.FaultPlan{Mode: ModeOnce, Call: 2, Status: 503}, []int{200, 503, 200, 200}},
		{"once at 5 never fires", schemas.FaultPlan{Mode: ModeOnce, Call: 5, Status: 503}, []int{200, 200, 200, 200}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, UpstreamScripted, "")
			tc.plan.RunID, tc.plan.Nonce = "run-"+tc.name, "nonce_x"
			env.setPlan(t, "slot-a", tc.plan)
			for i, want := range tc.want {
				// uncounted calls between conversation calls never shift the count
				env.call(t, http.MethodPost, "/v1/messages/count_tokens", "auth-a", msgBody)
				resp, body := env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody)
				if resp.StatusCode != want {
					t.Errorf("call %d: status %d, want %d: %s", i+1, resp.StatusCode, want, body)
				}
			}
			// slot b shares nothing with slot a
			if resp, _ := env.call(t, http.MethodPost, "/v1/messages", "auth-b", msgBody); resp.StatusCode != 200 {
				t.Errorf("slot-b status %d, want 200", resp.StatusCode)
			}
		})
	}
}

func TestPlanResetRestartsCount(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	plan := schemas.FaultPlan{Mode: ModeOnce, Call: 2, Status: 503, RunID: "r1", Nonce: "n1"}
	env.setPlan(t, "slot-a", plan)
	env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody)
	plan.RunID = "r2"
	env.setPlan(t, "slot-a", plan)
	if resp, _ := env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody); resp.StatusCode != 200 {
		t.Fatalf("first call after reset: status %d, want 200", resp.StatusCode)
	}
	if resp, _ := env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody); resp.StatusCode != 503 {
		t.Fatalf("second call after reset: status %d, want 503", resp.StatusCode)
	}
	if err := env.client.ClearPlan(context.Background(), "slot-a"); err != nil {
		t.Fatal(err)
	}
	resp, _ := env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody)
	entries := env.entries(t)
	last := entries[len(entries)-1]
	if resp.StatusCode != 200 || last.RunID != "" || last.Call != 1 {
		t.Fatalf("after clear: status %d, entry run %q call %d", resp.StatusCode, last.RunID, last.Call)
	}
}

func TestFaultEnvelope(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		retryAfter int
		wantType   string
	}{
		{"503 overloaded with retry-after", 503, 2, "overloaded_error"},
		{"529 overloaded", 529, 0, "overloaded_error"},
		{"500 api error", 500, 0, "api_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, UpstreamScripted, "")
			env.setPlan(t, "slot-a", schemas.FaultPlan{
				Mode: ModePersistent, FromCall: 1, Status: tc.status, RetryAfterS: tc.retryAfter,
				RunID: "r1", Nonce: "nonce_secret123",
			})
			resp, body := env.call(t, http.MethodPost, "/v1/messages", "auth-a", `{"model":"m","stream":true}`)
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.status)
			}
			if got := resp.Header.Get("retry-after"); (tc.retryAfter > 0) != (got != "") || (got != "" && got != "2") {
				t.Errorf("retry-after %q for retry_after_s %d", got, tc.retryAfter)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("content-type %q", ct)
			}
			e := decodeError(t, body)
			if e.Type != "error" || e.Error.Type != tc.wantType {
				t.Errorf("envelope type %q/%q, want error/%s", e.Type, e.Error.Type, tc.wantType)
			}
			if !strings.Contains(e.Error.Message, "ref nonce_secret123 call 1") {
				t.Errorf("message %q lacks the nonce", e.Error.Message)
			}
			entries := env.entries(t)
			last := entries[len(entries)-1]
			if last.Action != ActionInject || last.Status != tc.status || last.Nonce != "nonce_secret123" ||
				last.RunID != "r1" || last.Call != 1 || !last.Stream || last.Tokens != nil || last.ResponseID != "" {
				t.Errorf("ledger entry = %+v", last)
			}
		})
	}
}

// sseUpstream is a fake Anthropic API that streams in two halves: it sends
// message_start, then waits for release before sending the rest.
func sseUpstream(t *testing.T, release <-chan struct{}, seen chan<- *http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(context.Background())
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("request-id", "req_up")
			testWrite(t, w, upstreamJSON)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		testWrite(t, w, sseFirst)
		w.(http.Flusher).Flush()
		<-release
		testWrite(t, w, sseRest)
	}))
}

const (
	sseFirst = "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_up123\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-6\",\"content\":[],\"usage\":{\"input_tokens\":11,\"cache_read_input_tokens\":5,\"cache_creation_input_tokens\":7,\"output_tokens\":1}}}\r\n\r\n"
	sseRest  = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":42}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	upstreamJSON = `{"id":"msg_json9","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":2}}`
)

func TestStreamingPassThrough(t *testing.T) {
	release := make(chan struct{})
	seen := make(chan *http.Request, 4)
	up := sseUpstream(t, release, seen)
	defer up.Close()
	env := newTestEnv(t, up.URL, "sk-real-key")
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeNone, RunID: "r1"})

	req, err := http.NewRequest(http.MethodPost, env.data.URL+"/v1/messages?beta=true", strings.NewReader(`{"model":"claude-sonnet-4-6","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", "auth-a")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "tools-2024")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("x-secret-extra", "do-not-forward")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// message_start must arrive while the upstream is still holding the rest.
	br := bufio.NewReader(resp.Body)
	first := make([]byte, len(sseFirst))
	gotFirst := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(br, first)
		gotFirst <- err
	}()
	select {
	case err := <-gotFirst:
		if err != nil {
			t.Fatalf("read first chunk: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first SSE chunk was buffered, not flushed")
	}
	close(release)
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	if string(first)+string(rest) != sseFirst+sseRest {
		t.Fatalf("stream altered:\n%q", string(first)+string(rest))
	}

	up1 := <-seen
	checkForwarded(t, up1, map[string]string{
		"X-Api-Key": "sk-real-key", "Anthropic-Version": "2023-06-01", "Anthropic-Beta": "tools-2024",
		"Accept": "text/event-stream", "X-Secret-Extra": "", "Authorization": "",
	})
	if up1.URL.Path != "/v1/messages" || up1.URL.RawQuery != "beta=true" {
		t.Errorf("upstream path %s?%s", up1.URL.Path, up1.URL.RawQuery)
	}

	entries := env.entries(t)
	last := entries[len(entries)-1]
	if last.ResponseID != "msg_up123" || !last.Stream || last.Action != ActionPass || last.Status != 200 {
		t.Errorf("ledger entry = %+v", last)
	}
	want := schemas.Tokens{Fresh: 11, CacheRead: 5, CacheWrite: 7, Out: 42}
	if last.Tokens == nil || *last.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", last.Tokens, want)
	}
}

func TestRealNonStreamingPassThrough(t *testing.T) {
	seen := make(chan *http.Request, 4)
	up := sseUpstream(t, nil, seen)
	defer up.Close()
	env := newTestEnv(t, up.URL+"/", "sk-real-key")
	resp, body := env.call(t, http.MethodPost, "/v1/messages", "auth-b", msgBody)
	if resp.StatusCode != 200 || string(body) != upstreamJSON {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if resp.Header.Get("request-id") != "req_up" {
		t.Errorf("upstream request-id header not passed back")
	}
	checkForwarded(t, <-seen, map[string]string{"X-Api-Key": "sk-real-key", "Content-Type": "application/json"})
	entries := env.entries(t)
	last := entries[len(entries)-1]
	want := schemas.Tokens{Fresh: 3, CacheWrite: 2, Out: 4}
	if last.ResponseID != "msg_json9" || last.Stream || last.Tokens == nil || *last.Tokens != want || last.Slot != "slot-b" {
		t.Errorf("ledger entry = %+v tokens %+v", last, last.Tokens)
	}
}

func TestUpstreamUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	env := newTestEnv(t, "http://"+addr, "sk-real-key")
	resp, body := env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "sk-real-key") || decodeError(t, body).Error.Type != "api_error" {
		t.Errorf("body %s", body)
	}
	if !strings.Contains(string(body), utils.ProxyUnreachable) {
		t.Errorf("body %s lacks %q", body, utils.ProxyUnreachable)
	}
	entries := env.entries(t)
	last := entries[len(entries)-1]
	if last.Action != ActionPass || last.Status != http.StatusBadGateway || !strings.Contains(last.UpstreamError, "connection refused") ||
		strings.Contains(last.UpstreamError, "sk-real-key") || strings.Contains(last.UpstreamError, "/v1/messages") {
		t.Errorf("ledger entry = %+v", last)
	}
}

// TestUpstreamErrorOnlyWhenUnreachable checks that upstream_error marks only
// calls the provider never answered: a provider's own 502 and a caller that
// went away before the provider answered leave it empty.
func TestUpstreamErrorOnlyWhenUnreachable(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, _ := io.ReadAll(r.Body); strings.Contains(string(b), "hang") {
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"provider overloaded"}}`)
	}))
	defer up.Close()
	defer close(release)
	env := newTestEnv(t, up.URL, "sk-real-key")
	if resp, _ := env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d", resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.data.URL+"/v1/messages", strings.NewReader(strings.Replace(msgBody, "hi", "hang", 1)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", "auth-a")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("hanging upstream answered")
	}
	var entries []schemas.LedgerEntry
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if entries = env.entries(t); len(entries) == 2 {
			break
		}
	}
	if len(entries) != 2 {
		t.Fatalf("ledger has %d entries, want 2", len(entries))
	}
	for i, e := range entries {
		if e.UpstreamError != "" || e.Action != ActionPass {
			t.Errorf("entry %d = %+v, want a pass line with no upstream_error", i+1, e)
		}
	}
}

func TestNewServerConfig(t *testing.T) {
	dir := t.TempDir()
	good := []SlotConfig{{Name: "a", Auth: "x"}}
	cases := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"scripted", Config{LedgerPath: filepath.Join(dir, "1.jsonl"), Slots: good}, true},
		{"real", Config{Upstream: "https://api.anthropic.com", UpstreamKey: "k", LedgerPath: filepath.Join(dir, "2.jsonl"), Slots: good}, true},
		{"real without key", Config{Upstream: "https://api.anthropic.com", LedgerPath: filepath.Join(dir, "3.jsonl"), Slots: good}, false},
		{"bad upstream", Config{Upstream: "ftp://x", UpstreamKey: "k", LedgerPath: filepath.Join(dir, "4.jsonl"), Slots: good}, false},
		{"no slots gets defaults", Config{LedgerPath: filepath.Join(dir, "5.jsonl")}, true},
		{"slot of unknown provider", Config{LedgerPath: filepath.Join(dir, "8.jsonl"), Slots: []SlotConfig{{Name: "a", Auth: "x", Provider: "nope"}}}, false},
		{"bad provider table", Config{LedgerPath: filepath.Join(dir, "9.jsonl"), Slots: good, Providers: []ProviderSpec{{Name: "x"}}}, false},
		{"duplicate auth", Config{LedgerPath: filepath.Join(dir, "6.jsonl"), Slots: []SlotConfig{{Name: "a", Auth: "x"}, {Name: "b", Auth: "x"}}}, false},
		{"no ledger", Config{Slots: good}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := NewServer(tc.cfg)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok %v", err, tc.ok)
			}
			if srv != nil {
				if srv.ControlToken() == "" {
					t.Error("no control token generated")
				}
				if err := srv.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestServeShutsDownOnCancel(t *testing.T) {
	srv, err := NewServer(Config{LedgerPath: filepath.Join(t.TempDir(), "l.jsonl"), Slots: []SlotConfig{{Name: "a", Auth: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	dataLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, dataLn, ctrlLn) }()
	c := NewClient("http://" + ctrlLn.Addr().String())
	deadline := time.Now().Add(5 * time.Second)
	for c.Health(context.Background()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("control plane never became healthy")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

type errorEnvelope struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

func decodeError(t *testing.T, body []byte) errorEnvelope {
	t.Helper()
	var e errorEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error envelope %s: %v", body, err)
	}
	return e
}

func checkForwarded(t *testing.T, r *http.Request, want map[string]string) {
	t.Helper()
	for name, v := range want {
		if got := r.Header.Get(name); got != v {
			t.Errorf("upstream header %s = %q, want %q", name, got, v)
		}
	}
}

// testWrite writes s from a handler goroutine, where only t.Error is safe.
func testWrite(t *testing.T, w io.Writer, s string) {
	if _, err := io.WriteString(w, s); err != nil {
		t.Errorf("fake upstream write: %v", err)
	}
}

// callAs sends one request with the slot credential in the given header
// (value used verbatim) and returns the response with its body read.
func (env *testEnv) callAs(t *testing.T, method, target, header, value, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, env.data.URL+target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if header != "" {
		req.Header.Set(header, value)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

func (env *testEnv) last(t *testing.T) schemas.LedgerEntry {
	t.Helper()
	all := env.entries(t)
	return all[len(all)-1]
}

const chatBody = `{"model":"gpt-5-mini","messages":[{"role":"user","content":"hi"}]}`

func TestOpenAISlotsAndCounting(t *testing.T) {
	env := newTestEnvConfig(t, Config{})
	env.setPlan(t, "oa-1", schemas.FaultPlan{Mode: ModeNone, RunID: "r1"})
	steps := []struct {
		method, path, header, value string
		status                      int
		slot, kind                  string
		call                        int
	}{
		{http.MethodGet, "/v1/models", "Authorization", "Bearer oa-auth-1", 200, "oa-1", KindModels, 0},
		{http.MethodPost, "/v1/chat/completions", "Authorization", "Bearer oa-auth-1", 200, "oa-1", KindMessage, 1},
		{http.MethodPost, "/v1/responses", "Authorization", "bearer oa-auth-1", 200, "oa-1", KindMessage, 2},
		{http.MethodPost, "/v1/responses/input_tokens", "Authorization", "Bearer oa-auth-1", 200, "oa-1", KindCountTokens, 0},
		{http.MethodGet, "/v1/models/gpt-5-mini", "Authorization", "Bearer oa-auth-1", 200, "oa-1", KindModels, 0},
		{http.MethodPost, "/v1/embeddings", "Authorization", "Bearer oa-auth-1", 404, "oa-1", KindOther, 0},
		{http.MethodPost, "/v1/responses/compact", "Authorization", "Bearer oa-auth-1", 404, "oa-1", KindOther, 0},
		{http.MethodPost, "/v1/chat/completions/", "Authorization", "Bearer oa-auth-1", 200, "oa-1", KindMessage, 3},
		{http.MethodPost, "/v1/chat/completions", "Authorization", "oa-auth-1", 401, "", KindMessage, 0},
		{http.MethodPost, "/v1/chat/completions", "x-goog-api-key", "oa-auth-1", 200, "oa-1", KindMessage, 4},
	}
	for i, st := range steps {
		resp, body := env.callAs(t, st.method, st.path, st.header, st.value, chatBody)
		last := env.last(t)
		if resp.StatusCode != st.status || last.Slot != st.slot || last.Kind != st.kind || last.Call != st.call {
			t.Errorf("step %d %s %s: status %d slot %q kind %s call %d: %s", i, st.method, st.path, resp.StatusCode, last.Slot, last.Kind, last.Call, body)
		}
		if st.status == 401 {
			var e struct {
				Error struct{ Type, Code string } `json:"error"`
			}
			if err := json.Unmarshal(body, &e); err != nil || e.Error.Code != "invalid_api_key" {
				t.Errorf("401 envelope %s", body)
			}
		}
	}
}

func TestFaultEnvelopesByFormat(t *testing.T) {
	cases := []struct {
		name, slot, header, value, path string
		status                          int
		check                           func(t *testing.T, body []byte)
	}{
		{"openai 503", "oa-1", "Authorization", "Bearer oa-auth-1", "/v1/chat/completions", 503, func(t *testing.T, body []byte) {
			var e struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Code    any    `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &e); err != nil || e.Error.Type != "server_error" || e.Error.Code != nil ||
				!strings.HasSuffix(e.Error.Message, " ref nonce_oa call 1") {
				t.Errorf("openai envelope %s", body)
			}
		}},
		{"openai 429", "oa-1", "Authorization", "Bearer oa-auth-1", "/v1/responses", 429, func(t *testing.T, body []byte) {
			if !strings.Contains(string(body), `"code":"rate_limit_exceeded"`) {
				t.Errorf("openai 429 envelope %s", body)
			}
		}},
		{"gemini 503", "gm-slot-1", "x-goog-api-key", "gm-slot-1", "/models/gemini-3-pro:streamGenerateContent?alt=sse", 503, func(t *testing.T, body []byte) {
			var e struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
					Status  string `json:"status"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &e); err != nil || e.Error.Code != 503 || e.Error.Status != "UNAVAILABLE" ||
				!strings.Contains(e.Error.Message, "ref nonce_oa call 1") {
				t.Errorf("gemini envelope %s", body)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnvConfig(t, Config{Slots: []SlotConfig{{Name: "oa-1", Auth: "oa-auth-1", Provider: ProviderOpenAI}}})
			env.setPlan(t, tc.slot, schemas.FaultPlan{Mode: ModeOnce, Call: 1, Status: tc.status, RetryAfterS: 3, RunID: "r1", Nonce: "nonce_oa"})
			resp, body := env.callAs(t, http.MethodPost, tc.path, tc.header, tc.value, chatBody)
			if resp.StatusCode != tc.status || resp.Header.Get("retry-after") != "3" {
				t.Fatalf("status %d retry-after %q", resp.StatusCode, resp.Header.Get("retry-after"))
			}
			tc.check(t, body)
			last := env.last(t)
			if last.Action != ActionInject || last.Nonce != "nonce_oa" || last.Slot != tc.slot || last.Kind != KindMessage {
				t.Errorf("ledger entry = %+v", last)
			}
		})
	}
}

func TestUnconfiguredProviderAnswers503(t *testing.T) {
	env := newTestEnvConfig(t, Config{})
	resp, body := env.callAs(t, http.MethodPost, "/v1/chat/completions", "Authorization", "Bearer groq-slot-1", chatBody)
	if resp.StatusCode != 503 || !strings.Contains(string(body), "no key configured for provider groq (set GROQ_API_KEY)") {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if last := env.last(t); last.Slot != "groq-slot-1" || last.Action != ActionPass || last.Status != 503 || last.Call != 1 {
		t.Errorf("ledger entry = %+v", last)
	}
}

// fakeUpstream records what it received and answers with body as type ct.
func fakeUpstream(t *testing.T, ct, body string, seen chan<- *http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(context.Background())
		w.Header().Set("Content-Type", ct)
		w.Header().Set("openai-organization", "org-secret")
		w.Header().Set("x-request-id", "req_up")
		testWrite(t, w, body)
	}))
}

func TestRealForwardByProvider(t *testing.T) {
	cases := []struct {
		name, provider, header, value, target, ct, body string
		wantHeader, wantKey, wantQuery                  string
		wantID                                          string
		want                                            *schemas.Tokens
	}{
		{"openai chat json", ProviderOpenAI, "Authorization", "Bearer oa-auth-1", "/v1/chat/completions", "application/json", openAIChatJSON,
			"Authorization", "Bearer sk-openai", "", "chatcmpl-up1", &schemas.Tokens{Fresh: 36, CacheRead: 64, Out: 30}},
		{"openai responses stream", ProviderOpenAI, "Authorization", "Bearer oa-auth-1", "/v1/responses", "text/event-stream", openAIRespStream,
			"Authorization", "Bearer sk-openai", "", "resp_up4", &schemas.Tokens{Fresh: 9, Out: 3}},
		{"gemini header auth", ProviderGemini, "x-goog-api-key", "gm-slot-1", "/models/gemini-3-pro:streamGenerateContent?alt=sse", "text/event-stream", geminiStream,
			"x-goog-api-key", "g-key", "alt=sse", "gm-up5", &schemas.Tokens{Fresh: 4, CacheRead: 6, Out: 6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan *http.Request, 4)
			up := fakeUpstream(t, tc.ct, tc.body, seen)
			defer up.Close()
			specs := DefaultProviders()
			for i := range specs {
				specs[i].BaseURL = up.URL + "/base"
			}
			keys := map[string]string{"OPENAI_API_KEY": "sk-openai", "GEMINI_API_KEY": "g-key"}
			env := newTestEnvConfig(t, Config{Providers: specs, Getenv: func(k string) string { return keys[k] }})
			resp, body := env.callAs(t, http.MethodPost, tc.target, tc.header, tc.value, chatBody)
			if resp.StatusCode != 200 || string(body) != tc.body {
				t.Fatalf("status %d body %q", resp.StatusCode, body)
			}
			if resp.Header.Get("openai-organization") != "" || resp.Header.Get("x-request-id") != "req_up" {
				t.Errorf("response headers %v", resp.Header)
			}
			r := <-seen
			if got := r.Header.Get(tc.wantHeader); got != tc.wantKey {
				t.Errorf("upstream %s = %q, want %q", tc.wantHeader, got, tc.wantKey)
			}
			for _, h := range []string{"X-Api-Key", "Authorization", "X-Goog-Api-Key"} {
				if h != http.CanonicalHeaderKey(tc.wantHeader) && r.Header.Get(h) != "" {
					t.Errorf("placeholder leaked upstream in %s", h)
				}
			}
			wantPath, _, _ := strings.Cut(tc.target, "?")
			if r.URL.Path != "/base"+wantPath || r.URL.RawQuery != tc.wantQuery {
				t.Errorf("upstream target %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			last := env.last(t)
			if last.ResponseID != tc.wantID || last.Tokens == nil || *last.Tokens != *tc.want || last.Action != ActionPass {
				t.Errorf("ledger entry = %+v tokens %+v", last, last.Tokens)
			}
		})
	}
}

func TestQueryAuthForward(t *testing.T) {
	seen := make(chan *http.Request, 2)
	up := fakeUpstream(t, "application/json", `{"responseId":"gm-q"}`, seen)
	defer up.Close()
	spec := ProviderSpec{Name: "gq", BaseURL: up.URL, KeyEnv: "GQ_KEY", Auth: AuthQuery, Format: FormatGemini,
		Message: []string{"POST /models/*:generateContent"}}
	env := newTestEnvConfig(t, Config{
		Providers: []ProviderSpec{spec}, Slots: []SlotConfig{{Name: "gq-1", Auth: "gq-auth", Provider: "gq"}},
		Getenv: func(k string) string { return map[string]string{"GQ_KEY": "real-q"}[k] },
	})
	resp, _ := env.callAs(t, http.MethodPost, "/models/m:generateContent?key=gq-auth&alt=json", "", "", "{}")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	r := <-seen
	if q := r.URL.Query(); q.Get("key") != "real-q" || q.Get("alt") != "json" || len(q["key"]) != 1 {
		t.Errorf("upstream query %s", r.URL.RawQuery)
	}
	if last := env.last(t); last.Slot != "gq-1" || last.ResponseID != "gm-q" {
		t.Errorf("ledger entry %+v", last)
	}
}

// TestLedgerUpstream checks the upstream label on every kind of ledger line:
// it names the provider's route (real API or scripted model), so injected
// and rejected lines carry it too, and a provider with neither has none.
func TestLedgerUpstream(t *testing.T) {
	inject := schemas.FaultPlan{Mode: ModeOnce, Call: 1, Status: 503, RunID: "r1", Nonce: "nonce_up"}
	cases := []struct {
		name                string
		real                bool // point every provider at a fake upstream and give anthropic and openai keys
		plan                *schemas.FaultPlan
		slot, header, value string
		path, body          string
		wantStatus          int
		wantAction          string
		want                string
	}{
		{"scripted anthropic pass", false, nil, "slot-a", "x-api-key", "auth-a", "/v1/messages", msgBody,
			200, ActionPass, schemas.LedgerUpstreamScripted},
		{"scripted openai pass", false, nil, "oa-1", "Authorization", "Bearer oa-auth-1", "/v1/chat/completions", chatBody,
			200, ActionPass, schemas.LedgerUpstreamScripted},
		{"scripted injected", false, &inject, "slot-a", "x-api-key", "auth-a", "/v1/messages", msgBody,
			503, ActionInject, schemas.LedgerUpstreamScripted},
		{"scripted rejected", false, nil, "", "x-api-key", "sk-unknown", "/v1/messages", msgBody,
			401, ActionReject, schemas.LedgerUpstreamScripted},
		{"real anthropic pass", true, nil, "slot-a", "x-api-key", "auth-a", "/v1/messages", msgBody,
			200, ActionPass, schemas.LedgerUpstreamReal},
		{"real openai pass", true, nil, "oa-1", "Authorization", "Bearer oa-auth-1", "/v1/chat/completions", chatBody,
			200, ActionPass, schemas.LedgerUpstreamReal},
		{"real injected", true, &inject, "slot-a", "x-api-key", "auth-a", "/v1/messages", msgBody,
			503, ActionInject, schemas.LedgerUpstreamReal},
		{"keyless ollama is real", true, nil, "ollama-slot-1", "Authorization", "Bearer ollama-slot-1", "/v1/chat/completions", chatBody,
			200, ActionPass, schemas.LedgerUpstreamReal},
		{"unconfigured groq has none", false, nil, "groq-slot-1", "Authorization", "Bearer groq-slot-1", "/v1/chat/completions", chatBody,
			503, ActionPass, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{}
			if tc.real {
				seen := make(chan *http.Request, 4)
				up := fakeUpstream(t, "application/json", upstreamJSON, seen)
				t.Cleanup(up.Close)
				specs := DefaultProviders()
				for i := range specs {
					specs[i].BaseURL = up.URL
				}
				keys := map[string]string{"ANTHROPIC_API_KEY": "sk-ant", "OPENAI_API_KEY": "sk-openai"}
				cfg = Config{Providers: specs, Getenv: func(k string) string { return keys[k] }}
			}
			env := newTestEnvConfig(t, cfg)
			if tc.plan != nil {
				env.setPlan(t, tc.slot, *tc.plan)
			}
			resp, body := env.callAs(t, http.MethodPost, tc.path, tc.header, tc.value, tc.body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.wantStatus, body)
			}
			last := env.last(t)
			if last.Slot != tc.slot || last.Action != tc.wantAction || last.Upstream != tc.want {
				t.Errorf("ledger slot %q action %q upstream %q, want %q %q %q", last.Slot, last.Action, last.Upstream,
					tc.slot, tc.wantAction, tc.want)
			}
		})
	}
}
