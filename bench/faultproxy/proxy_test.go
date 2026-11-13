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
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	srv, err := NewServer(Config{
		Upstream: upstream, UpstreamKey: key, LedgerPath: path,
		Slots: []SlotConfig{{Name: "slot-a", Auth: "auth-a"}, {Name: "slot-b", Auth: "auth-b"}},
	})
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
		{"no slots", Config{LedgerPath: filepath.Join(dir, "5.jsonl")}, false},
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
