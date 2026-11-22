package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMCPHeimdall answers the v1 routes the MCP tools call and records requests.
type fakeMCPHeimdall struct {
	mu       sync.Mutex
	requests []string
	auth     []string
	bodies   map[string]string
	polls    int
}

func (f *fakeMCPHeimdall) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}
	f.requests = append(f.requests, key)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	body, _ := io.ReadAll(r.Body)
	if f.bodies == nil {
		f.bodies = map[string]string{}
	}
	f.bodies[key] = string(body)
	send := func(status int, v string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, v)
	}
	switch key {
	case "GET /api/v1/agents":
		send(200, `{"mode":"live","agents":[{"id":"bot"}]}`)
	case "GET /api/v1/benchmarks":
		send(200, `{"mode":"live","suites":[{"id":"c1"}]}`)
	case "GET /api/v1/benchmarks/packs":
		send(200, `{"mode":"live","packs":[{"id":"coding"}]}`)
	case "GET /api/v1/agents/bot/compatibility":
		send(200, `{"mode":"live","agent":"bot","benchmarks":[{"id":"c1","ok":true}]}`)
	case "POST /api/v1/evaluations/estimate":
		send(200, `{"mode":"live","runs":16,"est_cost_usd":{"low":1,"high":2,"known":true}}`)
	case "POST /api/v1/evaluations":
		send(202, `{"mode":"live","id":"ev_1","status":"queued","children":[]}`)
	case "GET /api/v1/evaluations/ev_1":
		f.polls++
		status := "running"
		if f.polls > 1 {
			status = "done"
		}
		send(200, `{"mode":"live","id":"ev_1","status":"`+status+`","children":[{"benchmark":"c1","experiment_id":"e_1","status":"done"},{"benchmark":"s1","experiment_id":"","status":"skipped","skipped":"agent lacks memory"}]}`)
	case "GET /api/v1/evaluations/ev_1/report":
		send(200, `{"mode":"live","evaluation":"ev_1","benchmarks":[{"benchmark":"c1","arms":{"A":{"score":0.5,"runs":4}}}]}`)
	case "GET /api/v1/experiments/e_1/triage":
		send(200, `{"mode":"live","exp_id":"e_1","groups":[
			{"pattern":"pattern_loop","label":"Loops","description":"Repeats the same call","per_arm":{"A":{"num":3,"den":4}},"runs":{"A":["r_1","r_2","r_3"]}},
			{"pattern":"pattern_giveup","label":"Gives up","description":"Stops early","per_arm":{"A":{"num":1,"den":4}},"runs":{"A":["r_4"]}},
			{"pattern":"pattern_none","label":"None","description":"","per_arm":{},"runs":{}}]}`)
	case "GET /api/v1/runs/r_4":
		send(200, `{"mode":"live","slot":{"exp_id":"e_1","task":"t1","arm":"A"},"run":{"run_id":"r_4","exp_id":"e_1","outcome":"fail"},"grade":{"y":0},"gates":[]}`)
	case "GET /api/v1/runs/r_1/trace?format=jsonl":
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, strings.Repeat(`{"step":1}`+"\n", 3))
	case "GET /api/v1/runs/big/trace":
		_, _ = io.WriteString(w, strings.Repeat("x", mcpTraceCap+10))
	case "GET /api/v1/evaluations/ev_x/report", "GET /api/v1/evaluations/ev_x":
		send(403, `{"mode":"live","error":{"message":"this API token's scopes (read) do not allow this request: scope evaluate required","type":"forbidden"}}`)
	default:
		send(404, `{"mode":"live","error":{"message":"route not found: `+r.URL.Path+`","type":"not_found"}}`)
	}
}

// mcpSession runs the server over in-memory pipes and returns the answers
// to the given messages, in order.
func mcpSession(t *testing.T, base string, msgs ...string) []map[string]any {
	t.Helper()
	srv := &mcpServer{api: &heimdallAPI{base: base, token: "hm_tok", client: &http.Client{Timeout: 5 * time.Second}}, version: "test", poll: 5 * time.Millisecond}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.serve(ctx, strings.NewReader(strings.Join(msgs, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var answers []map[string]any
	dec := json.NewDecoder(&out)
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		answers = append(answers, m)
	}
	return answers
}

func call(id int, tool string, args string) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": json.RawMessage(args)}})
	return string(b)
}

// toolText is the text content of a tools/call answer and its isError.
func toolText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	content := res["content"].([]any)
	isErr, _ := res["isError"].(bool)
	return content[0].(map[string]any)["text"].(string), isErr
}

func TestMCPHandshakeAndTools(t *testing.T) {
	fake := &fakeMCPHeimdall{}
	hs := httptest.NewServer(fake)
	defer hs.Close()
	ans := mcpSession(t, hs.URL,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`not json`,
		call(5, "nope", `{}`),
	)
	if len(ans) != 6 {
		t.Fatalf("answers = %d: %v", len(ans), ans)
	}
	init := ans[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["serverInfo"].(map[string]any)["name"] != "heimdall" {
		t.Errorf("initialize = %v", init)
	}
	var names []string
	for _, tool := range ans[1]["result"].(map[string]any)["tools"].([]any) {
		tm := tool.(map[string]any)
		names = append(names, tm["name"].(string))
		if tm["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("%s schema %v", tm["name"], tm["inputSchema"])
		}
	}
	want := "connect_agent,draft_benchmark,estimate,explain_failure,first_look,get_report,get_trace,inspect_source,list_agents,list_benchmarks,publish_benchmark,run_evaluation,run_readiness,verify_agent"
	if strings.Join(names, ",") != want {
		t.Errorf("tools = %v", names)
	}
	if ans[3]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Errorf("unknown method = %v", ans[3])
	}
	if ans[4]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Errorf("parse error = %v", ans[4])
	}
	if ans[5]["error"].(map[string]any)["code"].(float64) != -32602 {
		t.Errorf("unknown tool = %v", ans[5])
	}
}

func TestMCPToolsCallTheV1API(t *testing.T) {
	fake := &fakeMCPHeimdall{}
	hs := httptest.NewServer(fake)
	defer hs.Close()
	spec := `{"name":"n","mode":"score","arms":[{"label":"A","agent":"bot","model":"m"}],"benchmarks":["c1"]}`
	ans := mcpSession(t, hs.URL,
		call(1, "list_agents", `{}`),
		call(2, "list_benchmarks", `{"agent":"bot"}`),
		call(3, "estimate", `{"spec":`+spec+`}`),
		call(4, "run_evaluation", `{"spec":`+spec+`,"wait":true,"wait_minutes":1}`),
		call(5, "get_report", `{"evaluation_id":"ev_1"}`),
		call(6, "get_trace", `{"run_id":"r_1","format":"jsonl"}`),
		call(7, "get_trace", `{"run_id":"big"}`),
		call(8, "get_report", `{"evaluation_id":"ev_x"}`),
		call(9, "estimate", `{}`),
	)
	if len(ans) != 9 {
		t.Fatalf("answers = %d", len(ans))
	}
	for i, want := range []string{`"bot"`, `"compatibility"`, `"runs": 16`, `"report"`, `"evaluation": "ev_1"`, `{"step":1}`, "... cut:"} {
		text, isErr := toolText(t, ans[i])
		if isErr || !strings.Contains(text, want) {
			t.Errorf("call %d: isError %v, text lacks %q: %.300s", i+1, isErr, want, text)
		}
	}
	if text, isErr := toolText(t, ans[3]); !strings.Contains(text, `"status": "done"`) || isErr {
		t.Errorf("run_evaluation did not wait: %.300s", text)
	}
	if text, isErr := toolText(t, ans[7]); !isErr || !strings.Contains(text, "scope evaluate required") {
		t.Errorf("scope refusal = %v %s", isErr, text)
	}
	if _, isErr := toolText(t, ans[8]); !isErr {
		t.Error("estimate without spec succeeded")
	}
	for i, a := range fake.auth {
		if a != "Bearer hm_tok" {
			t.Errorf("request %s auth %q", fake.requests[i], a)
		}
	}
	if !strings.Contains(fake.bodies["POST /api/v1/evaluations"], `"benchmarks":["c1"]`) {
		t.Errorf("posted spec = %s", fake.bodies["POST /api/v1/evaluations"])
	}
}

func TestMCPExplainFailureIsReadOnly(t *testing.T) {
	fake := &fakeMCPHeimdall{polls: 5}
	hs := httptest.NewServer(fake)
	defer hs.Close()
	ans := mcpSession(t, hs.URL,
		call(1, "explain_failure", `{"evaluation_id":"ev_1"}`),
		call(2, "explain_failure", `{"evaluation_id":"ev_1","run_id":"r_4"}`),
		call(3, "explain_failure", `{}`),
	)
	text, isErr := toolText(t, ans[0])
	if isErr {
		t.Fatal(text)
	}
	var out struct {
		Summary  string `json:"summary"`
		Patterns []struct {
			Pattern string `json:"pattern"`
			ThisRun bool   `json:"this_run"`
		} `json:"patterns"`
		Skipped []map[string]any `json:"skipped_benchmarks"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Patterns) != 2 || out.Patterns[0].Pattern != "pattern_loop" || !strings.Contains(out.Summary, "Loops") || len(out.Skipped) != 1 {
		t.Errorf("explain = %s", text)
	}
	text, _ = toolText(t, ans[1])
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Patterns[0].Pattern != "pattern_giveup" || !out.Patterns[0].ThisRun || !strings.Contains(text, `"outcome": "fail"`) {
		t.Errorf("run focus = %s", text)
	}
	if _, isErr := toolText(t, ans[2]); !isErr {
		t.Error("explain without ids succeeded")
	}
	for _, r := range fake.requests {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("explain_failure wrote: %s", r)
		}
	}
}
