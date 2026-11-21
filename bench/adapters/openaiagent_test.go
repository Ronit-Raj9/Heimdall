package adapters

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func openaiAgent() *schemas.AgentConfig {
	return &schemas.AgentConfig{
		ID: "my-endpoint", Adapter: AdapterOpenAIAgent, Image: "bench/helper:1", Model: "claude-test",
		Budgets: schemas.Budgets{WallS: 300, CPUs: 1, MemGB: 1},
		OpenAI:  &schemas.OpenAIAgent{URL: "https://agent.example.com/v1", APIKeySecret: "MY_AGENT_KEY", TimeoutS: 120},
	}
}

func openaiFixture(t *testing.T) (*schemas.RunContext, CommandConfig) {
	t.Helper()
	rc, cfg := fixture(t, openaiAgent())
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
	return rc, cfg
}

func TestOpenAIAgentPlan(t *testing.T) {
	rc, cfg := openaiFixture(t)
	plan, err := newOpenAIAgentAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Argv, " ") != "python3 -I "+openaiLauncherFile || plan.Network != testNetwork {
		t.Fatalf("argv %q network %q", plan.Argv, plan.Network)
	}
	if len(plan.Egress) != 1 || plan.Egress[0] != "agent.example.com:443" {
		t.Fatalf("egress %v", plan.Egress)
	}
	want := map[string]string{
		"BENCH_OPENAI_URL": "https://agent.example.com/v1/chat/completions", "BENCH_OPENAI_API": "chat",
		"BENCH_OPENAI_MODEL": "claude-test", "BENCH_OPENAI_TIMEOUT_S": "120", "BENCH_OPENAI_MAX_TURNS": "20",
		openaiEnvAPIKey: "{{secret.MY_AGENT_KEY}}", utils.EnvRemoteConnect: "bench-eg-r_p20fj3s8wa:9300",
		utils.EnvAnswerPath: "/workspace/answer.txt", "BENCH_OPENAI_SESSION_INDEX": "1",
	}
	for k, v := range want {
		if plan.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, plan.Env[k], v)
		}
	}
	for _, k := range []string{utils.EnvMCPServers, "BENCH_OPENAI_HISTORY", "BENCH_OPENAI_SYSTEM"} {
		if _, ok := plan.Env[k]; ok {
			t.Errorf("env %s set on a plain answer task", k)
		}
	}
	if plan.Files[openaiLauncherFile] != openaiLauncherSource || plan.Files[InstructionFile] != testInstruction {
		t.Fatal("launcher or instruction file missing")
	}
	if got := utils.RunSecretNames(rc); len(got) != 1 || got[0] != "MY_AGENT_KEY" {
		t.Fatalf("run secret names %v", got)
	}
}

func TestOpenAIAgentPlanToolsSessionsResponses(t *testing.T) {
	rc, cfg := openaiFixture(t)
	rc.Agent.OpenAI = &schemas.OpenAIAgent{URL: "http://agent.local:8000/v1/responses", API: schemas.OpenAIAPIResponses, Model: "my-agent", System: "Be brief.", MaxTurns: 5}
	rc.Task.Sessions = []schemas.SessionSpec{{ID: "s1", Instruction: "instruction.md"}, {ID: "s2", Instruction: "instruction.md"}}
	rc.Session, rc.SessionIndex = &rc.Task.Sessions[1], 1
	rc.Task.Output.Kind = schemas.OutputState
	rc.Services = []schemas.ServiceHandle{{Spec: schemas.ServiceSpec{ID: "crm", Role: schemas.ServiceRoleMCP}, URL: "http://toolproxy:9000/s/crm"}}
	plan, err := newOpenAIAgentAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env["BENCH_OPENAI_URL"] != "http://agent.local:8000/v1/responses" || plan.Env["BENCH_OPENAI_API"] != "responses" ||
		plan.Env["BENCH_OPENAI_MODEL"] != "my-agent" || plan.Env["BENCH_OPENAI_MAX_TURNS"] != "5" || plan.Egress[0] != "agent.local:8000" {
		t.Fatalf("env %v egress %v", plan.Env, plan.Egress)
	}
	if plan.Env["BENCH_OPENAI_HISTORY"] != openaiHistoryFile || plan.Env["BENCH_OPENAI_SESSION_ID"] != "s2" || plan.Env["BENCH_OPENAI_SESSION_INDEX"] != "2" {
		t.Fatalf("session env %v", plan.Env)
	}
	if !strings.Contains(plan.Env[utils.EnvMCPServers], "http://toolproxy:9000/s/crm/mcp") {
		t.Fatalf("mcp servers %q", plan.Env[utils.EnvMCPServers])
	}
	if plan.Files[openaiSystemFile] != "Be brief." || plan.Env["BENCH_OPENAI_SYSTEM"] != openaiSystemFile {
		t.Fatal("system message not planned")
	}
	if _, ok := plan.Env[openaiEnvAPIKey]; ok {
		t.Fatal("api key env set without api_key_secret")
	}
}

func TestOpenAIAgentPlanErrors(t *testing.T) {
	cases := []struct {
		name   string
		change func(rc *schemas.RunContext)
		want   string
	}{
		{"no block", func(rc *schemas.RunContext) { rc.Agent.OpenAI = nil }, "no openai block"},
		{"diff task", func(rc *schemas.RunContext) { rc.Task.Output = nil }, "cannot produce a diff output"},
		{"artifact task", func(rc *schemas.RunContext) { rc.Task.Output.Kind = schemas.OutputArtifact }, "cannot produce a artifact output"},
		{"state no tools", func(rc *schemas.RunContext) { rc.Task.Output.Kind = schemas.OutputState }, "no MCP tools"},
		{"state tools off", func(rc *schemas.RunContext) {
			rc.Task.Output.Kind = schemas.OutputState
			rc.Agent.OpenAI.Tools = schemas.OpenAIToolsOff
		}, "needs openai.tools auto"},
		{"bad url", func(rc *schemas.RunContext) { rc.Agent.OpenAI.URL = "ftp://x" }, "want http(s)"},
		{"bad api", func(rc *schemas.RunContext) { rc.Agent.OpenAI.API = "completions" }, "want chat or responses"},
		{"api mismatch", func(rc *schemas.RunContext) { rc.Agent.OpenAI.URL = "https://x/v1/responses" }, "openai.api is chat"},
		{"bad secret", func(rc *schemas.RunContext) { rc.Agent.OpenAI.APIKeySecret = "a-b" }, "not a secret name"},
		{"bad tools", func(rc *schemas.RunContext) { rc.Agent.OpenAI.Tools = "all" }, "want auto or off"},
		{"no paid", func(rc *schemas.RunContext) { rc.NoPaidTraffic = true }, "scripted model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc, cfg := openaiFixture(t)
			tc.change(rc)
			if _, err := newOpenAIAgentAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestOpenAIAgentBuiltin(t *testing.T) {
	for _, a := range Builtin(CommandConfig{}) {
		if a.GetName() == AdapterOpenAIAgent {
			return
		}
	}
	t.Fatal("openai_agent is not a built-in adapter")
}

// fakeMCP is a tool server on the run's tool proxy: one tool, lookup.
type fakeMCP struct {
	mu    sync.Mutex
	calls []map[string]any
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var msg map[string]any
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id, ok := msg["id"]
	if !ok {
		w.WriteHeader(202)
		return
	}
	var result any
	switch msg["method"] {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-1")
		result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}
	case "tools/list":
		result = map[string]any{"tools": []any{map[string]any{"name": "lookup", "description": "Look up an order.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"order": map[string]any{"type": "string"}}}}}}
	case "tools/call":
		f.mu.Lock()
		f.calls = append(f.calls, msg["params"].(map[string]any))
		f.mu.Unlock()
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "order 42 shipped on Tuesday"}}}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// fakeOpenAI answers chat/completions and responses: a tool call first when
// tools are offered and no tool result is in the request yet, then text.
type fakeOpenAI struct {
	mu       sync.Mutex
	auth     []string
	host     string
	bodies   []map[string]any
	status   int
	answerOf func(body map[string]any) string
}

func (f *fakeOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f.mu.Lock()
	f.auth, f.host, f.bodies = append(f.auth, r.Header.Get("Authorization")), r.Host, append(f.bodies, body)
	f.mu.Unlock()
	if f.status != 0 && f.status != 200 {
		http.Error(w, "boom", f.status)
		return
	}
	text := "Tuesday"
	if f.answerOf != nil {
		text = f.answerOf(body)
	}
	_, hasTools := body["tools"]
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(r.URL.Path, "/responses") {
		items, _ := body["input"].([]any)
		done := false
		for _, it := range items {
			if m, ok := it.(map[string]any); ok && m["type"] == "function_call_output" {
				done = true
			}
		}
		if hasTools && !done {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{
				map[string]any{"type": "function_call", "call_id": "fc_1", "name": "lookup", "arguments": `{"order":"42"}`}},
				"usage": map[string]any{"input_tokens": 50, "output_tokens": 5}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}},
			"usage": map[string]any{"input_tokens": 80, "output_tokens": 3}})
		return
	}
	msgs, _ := body["messages"].([]any)
	done := false
	for _, it := range msgs {
		if m, ok := it.(map[string]any); ok && m["role"] == "tool" {
			done = true
		}
	}
	if hasTools && !done {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "call_1", "type": "function",
				"function": map[string]any{"name": "lookup", "arguments": `{"order":"42"}`}}}}}},
			"usage": map[string]any{"prompt_tokens": 50, "completion_tokens": 5}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": text}}},
		"usage": map[string]any{"prompt_tokens": 80, "completion_tokens": 3, "prompt_tokens_details": map[string]any{"cached_tokens": 30}}})
}

// runOpenAILauncher runs openaiagent.py on the host with env, the
// instruction text and a memory folder dir (kept across calls by the caller).
func runOpenAILauncher(t *testing.T, dir, instruction string, env map[string]string) (string, string, error) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	script := filepath.Join(dir, "openaiagent.py")
	inst := filepath.Join(dir, "instruction.md")
	answer := filepath.Join(dir, "ws", "answer.txt")
	_ = os.Remove(answer)
	for p, c := range map[string]string{script: openaiLauncherSource, inst: instruction} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(py, "-I", script)
	cmd.Env = []string{"BENCH_OPENAI_INSTRUCTION=" + inst, "BENCH_ANSWER_PATH=" + answer}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	b, _ := os.ReadFile(answer)
	return string(b), string(out), err
}

func portOf(t *testing.T, u string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(u, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestOpenAIAgentLauncherChatTools(t *testing.T) {
	mcp := &fakeMCP{}
	mcpSrv := httptest.NewServer(mcp)
	defer mcpSrv.Close()
	ep := &fakeOpenAI{}
	srv := httptest.NewServer(ep)
	defer srv.Close()
	port := portOf(t, srv.URL)
	servers, _ := json.Marshal(map[string]any{"orders": map[string]any{"url": mcpSrv.URL + "/mcp"}})
	// The URL names a host that does not resolve: the launcher must connect
	// to BENCH_REMOTE_CONNECT (the forwarder) and keep the URL's host.
	env := map[string]string{
		"BENCH_OPENAI_URL": "http://agent.invalid:" + port + "/v1/chat/completions", utils.EnvRemoteConnect: "127.0.0.1:" + port,
		"BENCH_OPENAI_API": "chat", "BENCH_OPENAI_MODEL": "my-agent", "BENCH_OPENAI_API_KEY": "sk-user-agent-key-0123456789",
		"BENCH_OPENAI_TIMEOUT_S": "20", utils.EnvMCPServers: string(servers),
	}
	answer, out, err := runOpenAILauncher(t, t.TempDir(), "When did order 42 ship?", env)
	if err != nil || answer != "Tuesday" {
		t.Fatalf("answer %q err %v\n%s", answer, err, out)
	}
	if len(ep.bodies) != 2 || ep.host != "agent.invalid:"+port || ep.auth[0] != "Bearer sk-user-agent-key-0123456789" {
		t.Fatalf("requests %d host %q auth %v", len(ep.bodies), ep.host, ep.auth)
	}
	first := ep.bodies[0]
	tools, _ := first["tools"].([]any)
	if first["model"] != "my-agent" || len(tools) != 1 {
		t.Fatalf("first request %v", first)
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "lookup" || fn["description"] != "Look up an order." {
		t.Fatalf("tool def %v", fn)
	}
	if len(mcp.calls) != 1 || mcp.calls[0]["name"] != "lookup" || mcp.calls[0]["arguments"].(map[string]any)["order"] != "42" {
		t.Fatalf("mcp calls %v", mcp.calls)
	}
	msgs := ep.bodies[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "call_1" || last["content"] != "order 42 shipped on Tuesday" {
		t.Fatalf("tool result message %v", last)
	}
	if strings.Contains(out, "sk-user-agent-key") {
		t.Fatal("the launcher printed the API key")
	}
	types := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) == nil && ev["schema"] == "bifrost.agent.event" {
			types[ev["type"].(string)]++
			if ev["type"] == "model_response" && ev["tokens"] == nil {
				t.Errorf("model_response without tokens: %s", line)
			}
		}
	}
	if types["model_request"] != 2 || types["model_response"] != 2 || types["tool_call"] != 1 || types["tool_result"] != 1 || types["claim"] != 1 {
		t.Fatalf("events %v\n%s", types, out)
	}
}

func TestOpenAIAgentLauncherResponses(t *testing.T) {
	mcp := &fakeMCP{}
	mcpSrv := httptest.NewServer(mcp)
	defer mcpSrv.Close()
	ep := &fakeOpenAI{}
	srv := httptest.NewServer(ep)
	defer srv.Close()
	servers, _ := json.Marshal(map[string]any{"orders": map[string]any{"url": mcpSrv.URL + "/mcp"}})
	env := map[string]string{"BENCH_OPENAI_URL": srv.URL + "/v1/responses", "BENCH_OPENAI_API": "responses",
		"BENCH_OPENAI_MODEL": "m", "BENCH_OPENAI_TIMEOUT_S": "20", utils.EnvMCPServers: string(servers)}
	answer, out, err := runOpenAILauncher(t, t.TempDir(), "When did order 42 ship?", env)
	if err != nil || answer != "Tuesday" {
		t.Fatalf("answer %q err %v\n%s", answer, err, out)
	}
	if len(mcp.calls) != 1 || len(ep.bodies) != 2 || ep.auth[0] != "" {
		t.Fatalf("mcp calls %d requests %d auth %v", len(mcp.calls), len(ep.bodies), ep.auth)
	}
	items := ep.bodies[1]["input"].([]any)
	last := items[len(items)-1].(map[string]any)
	if last["type"] != "function_call_output" || last["call_id"] != "fc_1" || last["output"] != "order 42 shipped on Tuesday" {
		t.Fatalf("function_call_output %v", last)
	}
}

func TestOpenAIAgentLauncherSessions(t *testing.T) {
	ep := &fakeOpenAI{answerOf: func(body map[string]any) string {
		msgs := body["messages"].([]any)
		return "seen " + string(rune('0'+len(msgs)))
	}}
	srv := httptest.NewServer(ep)
	defer srv.Close()
	dir := t.TempDir()
	env := map[string]string{"BENCH_OPENAI_URL": srv.URL + "/v1/chat/completions", "BENCH_OPENAI_TIMEOUT_S": "20",
		"BENCH_OPENAI_HISTORY": filepath.Join(dir, "memory", "h.json"), "BENCH_OPENAI_SESSION_INDEX": "1"}
	if answer, out, err := runOpenAILauncher(t, dir, "My cat is called Miso.", env); err != nil || answer != "seen 1" {
		t.Fatalf("session 1: answer %q err %v\n%s", answer, err, out)
	}
	env["BENCH_OPENAI_SESSION_INDEX"] = "2"
	answer, out, err := runOpenAILauncher(t, dir, "What is my cat called?", env)
	if err != nil || answer != "seen 3" {
		t.Fatalf("session 2: answer %q err %v\n%s", answer, err, out)
	}
	msgs := ep.bodies[1]["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "My cat is called Miso." || msgs[1].(map[string]any)["content"] != "seen 1" {
		t.Fatalf("session 2 did not carry session 1: %v", msgs)
	}
	if _, ok := ep.bodies[1]["tools"]; ok {
		t.Fatal("tools offered without MCP servers")
	}
}

func TestOpenAIAgentLauncherHTTPError(t *testing.T) {
	ep := &fakeOpenAI{status: 500}
	srv := httptest.NewServer(ep)
	defer srv.Close()
	answer, out, err := runOpenAILauncher(t, t.TempDir(), "hi", map[string]string{"BENCH_OPENAI_URL": srv.URL + "/v1/chat/completions", "BENCH_OPENAI_TIMEOUT_S": "5"})
	if err == nil || answer != "" || !strings.Contains(out, "HTTP 500") {
		t.Fatalf("answer %q err %v\n%s", answer, err, out)
	}
}
