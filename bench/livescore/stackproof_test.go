//go:build integration

package livescore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/gateways/bifrost"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// TestIntegrationLiveStack is the $0 proof on a throwaway Bifrost v2.5.1
// stack whose fault proxy holds no provider key (every call is answered by
// the scripted model). It needs a running stack (docs/live-scoring.md,
// "Proof at $0"):
//
//	HDX6_URL           Bifrost, e.g. http://127.0.0.1:28400
//	HDX6_SETUP_TOKEN   its setup token
//	HDX6_FP_URL        the fault proxy control API, e.g. http://127.0.0.1:28401
//	HDX6_FP_TOKEN      its control token
//	HDX6_OUT           a folder for the scores, the inbox and the evidence
//
// The test itself sets up the stack (a provider and four virtual keys, as
// the bench does for runs) and sends scripted traffic; live scoring then
// reads it with GET requests only. Two healthy-vs-faulty versions selected
// by virtual key must decide worse; two healthy versions selected by a
// header tag (an A/A test) must not decide.
func TestIntegrationLiveStack(t *testing.T) {
	url, token := os.Getenv("HDX6_URL"), os.Getenv("HDX6_SETUP_TOKEN")
	fpURL, fpToken, out := os.Getenv("HDX6_FP_URL"), os.Getenv("HDX6_FP_TOKEN"), os.Getenv("HDX6_OUT")
	if url == "" || token == "" || fpURL == "" || out == "" {
		t.Skip("set HDX6_URL, HDX6_SETUP_TOKEN, HDX6_FP_URL, HDX6_FP_TOKEN and HDX6_OUT")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	const model = "claude-haiku-4-5"
	slots := []bifrost.Slot{{Name: "fp-slot-1", Value: "fp-slot-1"}, {Name: "fp-slot-2", Value: "fp-slot-2"}, {Name: "fp-slot-3", Value: "fp-slot-3"}, {Name: "fp-slot-4", Value: "fp-slot-4"}}
	gw, err := bifrost.New(bifrost.Config{URL: url, SetupToken: token, FaultProxyURL: "http://faultproxy:9100", MaxRetries: 1,
		Providers: []bifrost.ProviderSetup{{Name: "anthropic", Mode: bifrost.ModeProxied, Model: model, Slots: slots}}, Logger: tLogger{t}})
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Setup(ctx); err != nil {
		t.Fatalf("setup: %v", err)
	}
	fp := faultproxy.NewClient(fpURL, faultproxy.WithToken(fpToken))
	keys := make([]*schemas.RunKey, 4)
	for i := range keys {
		k, err := gw.MintKey(ctx, &schemas.RunContext{Run: &schemas.Run{RunID: fmt.Sprintf("r_hdx6live%d", i)}})
		if err != nil {
			t.Fatalf("mint key %d: %v", i, err)
		}
		keys[i] = k
		t.Cleanup(func() { _ = gw.RevokeKey(context.Background(), k) })
	}
	t.Logf("keys: A %s (%s), B %s (%s), A/A %s (%s) and %s (%s)", keys[0].ID, keys[0].KeySlot, keys[1].ID, keys[1].KeySlot,
		keys[2].ID, keys[2].KeySlot, keys[3].ID, keys[3].KeySlot)
	n := 70
	tag := fmt.Sprintf("t%d", time.Now().Unix()) // header values unique to this run, so rows of an earlier run never match
	started := time.Now().UTC()
	for i := range n {
		// A: healthy. B: every second session's second request answers 503
		// (and its retry), so the session ends on an error; every fourth
		// session's tool result says it failed.
		drive(ctx, t, url, fp, keys[0], "v1", fmt.Sprintf("hdx6-a-%d", i), model, false, false)
		drive(ctx, t, url, fp, keys[1], "v2", fmt.Sprintf("hdx6-b-%d", i), model, i%2 == 0, i%4 == 1)
		// A/A: both healthy, selected by a header tag.
		drive(ctx, t, url, fp, keys[2], tag+"-aa1", fmt.Sprintf("hdx6-aa1-%s-%d", tag, i), model, false, false)
		drive(ctx, t, url, fp, keys[3], tag+"-aa2", fmt.Sprintf("hdx6-aa2-%s-%d", tag, i), model, false, false)
	}
	// Gap grouping on real rows: three sessions with no session id on one
	// key, 32 s apart (the smallest gap is 30 s), each of two requests.
	for i := range 3 {
		if i > 0 {
			time.Sleep(32 * time.Second)
		}
		drive(ctx, t, url, fp, keys[2], tag+"-gap", "", model, false, false)
	}
	time.Sleep(4 * time.Second) // Bifrost's batched log writer
	// Live scoring reads the stack: GET only, recorded.
	rec := &recordingTransport{next: http.DefaultTransport}
	hc := &http.Client{Transport: rec, Timeout: 30 * time.Second}
	cfgAB := fmt.Sprintf(`
source: {url: %s, token_env: HDX6_SETUP_TOKEN, window: 2h}
versions:
  - name: v1
    virtual_keys: [%s]
  - name: v2
    virtual_keys: [%s]
compare: {baseline: v1, candidate: v2}
sessions: {gap: 30s}
thresholds:
  - {signal: abandoned, above: 0.2, min_sessions: 20}
inbox: {max_per_pull: 3}
`, url, keys[0].ID, keys[1].ID)
	cfgAA := fmt.Sprintf(`
source: {url: %s, token_env: HDX6_SETUP_TOKEN, window: 2h}
versions:
  - name: aa-1
    tag: {header: x-bf-lh-agent-version, value: %[2]s-aa1}
  - name: aa-2
    tag: {header: x-bf-lh-agent-version, value: %[2]s-aa2}
compare: {baseline: aa-1, candidate: aa-2}
sessions: {gap: 30s}
`, url, tag)
	cfgGap := fmt.Sprintf(`
source: {url: %s, token_env: HDX6_SETUP_TOKEN, window: 2h}
versions:
  - name: gap
    tag: {header: x-bf-lh-agent-version, value: %[2]s-gap}
sessions: {gap: 30s}
`, url, tag)
	later := time.Now().UTC().Add(2 * time.Minute) // every session idle past the gap
	for _, run := range []struct {
		name, yml, want string
		perVersion      int
	}{{"ab", cfgAB, ResultWorse, n}, {"aa", cfgAA, ResultUnknown, n}, {"gap", cfgGap, "", 3}} {
		cfg, err := ParseConfig([]byte(run.yml), run.name)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(out, run.name)
		_ = os.RemoveAll(dir)
		inbox := traceinbox.NewFileStore(filepath.Join(dir, "inbox"))
		s := &Scorer{Config: cfg, Store: NewStore(filepath.Join(dir, Dir)), Inbox: inbox, HTTP: hc,
			Getenv: func(k string) string { return os.Getenv(k) }, Now: func() time.Time { return later }}
		res, err := s.Pull(ctx)
		if err != nil {
			t.Fatalf("%s pull: %v", run.name, err)
		}
		cmp, _ := s.Compare()
		if cmp == nil {
			cmp = &Comparison{}
		}
		st, _ := s.Status(later)
		writeJSON(t, filepath.Join(dir, "pull.json"), res)
		writeJSON(t, filepath.Join(dir, "compare.json"), cmp)
		writeJSON(t, filepath.Join(dir, "status.json"), st)
		t.Logf("%s: rows %v, sessions %v, open %d, inbox %v, decided %v, grouping %+v", run.name, res.Rows, res.Sessions, res.Open, res.Inbox, res.Decided, st.Grouping)
		t.Logf("%s: %s", run.name, cmp.summaryLine())
		for _, v := range cfg.Versions {
			if res.Sessions[v.Name] != run.perVersion {
				t.Errorf("%s: version %s scored %d sessions, want %d", run.name, v.Name, res.Sessions[v.Name], run.perVersion)
			}
		}
		if run.name == "gap" && (st.Grouping.ByGap != 3 || st.Grouping.Concurrent != 0) {
			t.Errorf("gap: grouping %+v, want 3 gap sessions, none concurrent", st.Grouping)
		}
		if run.want != "" && cmp.Result != run.want {
			t.Errorf("%s: comparison %s, want %s", run.name, cmp.Result, run.want)
		}
		if run.name == "ab" {
			items, _ := inbox.Items()
			if len(items) == 0 {
				t.Error("ab: no inbox item")
			}
			for _, it := range items {
				t.Logf("inbox item %s: %s, %d occurrences, flagged %v", it.ID, it.SourceKind, it.Occurrences, it.Flagged)
			}
		}
	}
	methods := rec.seen()
	for _, m := range methods {
		if !strings.HasPrefix(m, "GET /api/logs") {
			t.Errorf("live scoring sent %s; only GET /api/logs is allowed", m)
		}
	}
	t.Logf("live scoring sent %d requests, every one a GET to the log API; traffic from %s", len(methods), started.Format(time.RFC3339))
	// Field evidence for the docs: one list row and one detail row of B.
	saveRowEvidence(ctx, t, url, token, keys[1].ID, filepath.Join(out, "row-evidence.json"))
}

// drive sends one scripted session: a request with a tool, the tool
// result, and the final request. fail503 arms a 503 on the second request
// (and Bifrost's retry); toolErr makes the tool result an error.
func drive(ctx context.Context, t *testing.T, url string, fp *faultproxy.Client, key *schemas.RunKey, version, sid, model string, fail503, toolErr bool) {
	t.Helper()
	plan := schemas.FaultPlan{Mode: "none", RunID: "r_hdx6live"}
	if fail503 {
		plan = schemas.FaultPlan{Mode: "persistent", FromCall: 2, Status: 503, RunID: "r_hdx6live"}
	}
	if err := fp.SetPlan(ctx, key.KeySlot, plan); err != nil { // resets the slot's counters: one tool turn per session
		t.Fatalf("set plan: %v", err)
	}
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup_order", "description": "Look up an order",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{"order": map[string]any{"type": "integer"}}, "required": []string{"order"}}}}}
	msgs := []map[string]any{{"role": "system", "content": "You are a support agent."},
		{"role": "user", "content": "Where is my order 12? My email is alice@example.com."}}
	resp := chat(ctx, t, url, key.Value, version, sid, model, msgs, tools)
	if resp == nil {
		return
	}
	var call map[string]any
	if ch, _ := resp["choices"].([]any); len(ch) > 0 {
		m, _ := ch[0].(map[string]any)["message"].(map[string]any)
		msgs = append(msgs, m)
		if tc, _ := m["tool_calls"].([]any); len(tc) > 0 {
			call, _ = tc[0].(map[string]any)
		}
	}
	if call == nil {
		t.Logf("session %s: no tool call in the scripted answer", sid)
		return
	}
	result := `{"order": 12, "status": "shipped"}`
	if toolErr {
		result = "Error: order service timed out"
	}
	msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": call["id"], "content": result})
	_ = chat(ctx, t, url, key.Value, version, sid, model, msgs, tools)
}

func chat(ctx context.Context, t *testing.T, url, key, version, sid, model string, msgs, tools any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"model": "anthropic/" + model, "messages": msgs, "tools": tools, "max_tokens": 200})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("x-bf-lh-agent-version", version)
	if sid != "" {
		req.Header.Set("x-bf-session-id", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil // an injected fault: the session ends here
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

type recordingTransport struct {
	next http.RoundTripper
	mu   sync.Mutex
	reqs []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req.Method+" "+req.URL.Path)
	r.mu.Unlock()
	return r.next.RoundTrip(req)
}

func (r *recordingTransport) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reqs...)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.MarshalIndent(v, "", "  ")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// saveRowEvidence keeps the field names (not the content) of one list row
// and one detail row, so the docs can say which fields v2.5.1 serves.
func saveRowEvidence(ctx context.Context, t *testing.T, url, token, vk, path string) {
	c := newClient(url, token, nil)
	var page struct {
		Logs []map[string]any `json:"logs"`
	}
	if err := c.get(ctx, "/api/logs?limit=1&virtual_key_ids="+vk+"&status=error", &page); err != nil || len(page.Logs) == 0 {
		t.Logf("row evidence: %v", err)
		return
	}
	var detail map[string]any
	_ = c.get(ctx, "/api/logs/"+fmt.Sprint(page.Logs[0]["id"]), &detail)
	keysOf := func(m map[string]any) []string {
		var ks []string
		for k, v := range m {
			if v != nil {
				ks = append(ks, k)
			}
		}
		return ks
	}
	ev := map[string]any{"list_row_fields": keysOf(page.Logs[0]), "detail_row_fields": keysOf(detail),
		"list_metadata": page.Logs[0]["metadata"], "list_session_id_set": page.Logs[0]["session_id"] != nil,
		"detail_error_details": detail["error_details"], "list_latency": page.Logs[0]["latency"], "list_number_of_retries": page.Logs[0]["number_of_retries"]}
	writeJSON(t, path, ev)
}

type tLogger struct{ t *testing.T }

func (l tLogger) Debug(string, ...any)       {}
func (l tLogger) Info(m string, a ...any)    { l.t.Log(append([]any{m}, a...)...) }
func (l tLogger) Warn(m string, a ...any)    { l.t.Log(append([]any{"WARN " + m}, a...)...) }
func (l tLogger) Error(m string, a ...any)   { l.t.Log(append([]any{"ERROR " + m}, a...)...) }
func (l tLogger) With(...any) schemas.Logger { return l }
