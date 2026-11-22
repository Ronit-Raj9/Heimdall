package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/utils"
)

// Y11: bench-driven conversations, run against the real user simulator
// (simuser.py) and fake agents over httptest, one per agent kind.

const convUserFile = `{"id": "refund-in-policy",
 "script": {"opening": "Hi, my burr grinder arrived with a cracked hopper.",
            "replies": [{"when": ["which order", "order number"], "say": "It is order B-3001."},
                        {"when": ["refunded", "refund is issued"], "say": "Great, thank you!", "stop": true}],
            "done": "No, that is all.", "default": "Okay."}}`

// startSimuser serves simuser.py on a free port; it returns the MCP URL and
// the transcript path.
func startSimuser(t *testing.T) (string, string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "simuser.py")
	if err := os.WriteFile(script, simuser.Script(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "user.json")
	if err := os.WriteFile(cfg, []byte(convUserFile), 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cmd := exec.Command(py, "-I", script, "serve", "--config", cfg, "--state", state, "--port", strconv.Itoa(port))
	cmd.Env = []string{"BENCH_SIM_MODE=scripted"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	for i := 0; ; i++ {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		if i > 100 {
			t.Fatalf("simuser did not start: %v\n%s", err, stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	return base + "/mcp", filepath.Join(state, "transcript.json")
}

type transcript struct {
	Driver    string `json:"driver"`
	EndedBy   string `json:"ended_by"`
	UserTurns int    `json:"user_turns"`
	Events    []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Origin  string `json:"origin"`
	} `json:"events"`
}

func readTranscript(t *testing.T, path string) transcript {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tr transcript
	if err := json.Unmarshal(b, &tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

// checkRefundChat asserts the scripted refund conversation ran to the
// customer's stop with the bench's greeting first.
func checkRefundChat(t *testing.T, tr transcript, agentLines ...string) {
	t.Helper()
	if tr.Driver != "bench" || tr.EndedBy != simuser.StopToken {
		t.Fatalf("driver %q ended_by %q", tr.Driver, tr.EndedBy)
	}
	if len(tr.Events) != 6 || tr.Events[0].Origin != "bench_greeting" || tr.Events[0].Content != schemas.DefaultConversationGreeting {
		t.Fatalf("events %+v", tr.Events)
	}
	for i, want := range agentLines {
		if ev := tr.Events[2+2*i]; ev.Role != "agent" || ev.Content != want {
			t.Fatalf("agent line %d = %+v, want %q", i, ev, want)
		}
	}
	if tr.Events[1].Content != "Hi, my burr grinder arrived with a cracked hopper." || !strings.Contains(tr.Events[5].Content, simuser.StopToken) {
		t.Fatalf("user lines %+v", tr.Events)
	}
}

// runPy runs one launcher with the shared helpers beside it.
func runPy(t *testing.T, launcher string, env map[string]string) (string, string, error) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	files := map[string]string{filepath.Join(dir, launcher): LauncherSource(launcher), filepath.Join(dir, "heimdall_conv.py"): convHelperSource,
		filepath.Join(dir, "instruction.md"): "Follow the support policy. Refund damaged orders.", filepath.Join(ws, "README.md"): "scratch\n"}
	for p, c := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	answer := filepath.Join(ws, "answer.txt")
	cmd := exec.Command(py, "-I", filepath.Join(dir, launcher))
	cmd.Env = []string{"BENCH_ANSWER_PATH=" + answer}
	for _, k := range []string{"BENCH_REMOTE_INSTRUCTION", "BENCH_OPENAI_INSTRUCTION", "BENCH_A2A_INSTRUCTION", "BENCH_PULL_INSTRUCTION"} {
		cmd.Env = append(cmd.Env, k+"="+filepath.Join(dir, "instruction.md"))
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+strings.ReplaceAll(v, "{ws}", ws))
	}
	out, err := cmd.CombinedOutput()
	b, _ := os.ReadFile(answer)
	return string(b), string(out), err
}

func convEnv(mcp string) map[string]string {
	users, _ := json.Marshal(map[string]any{"customer": map[string]string{"url": mcp}})
	return map[string]string{"BENCH_CONV_USERS": string(users), "BENCH_CONV_MAX_TURNS": "6", "BENCH_CONV_HISTORY": "client",
		"BENCH_CONV_GREETING": schemas.DefaultConversationGreeting}
}

// chatReply is the fake support agent: it asks for the order, then refunds.
func chatReply(user string) string {
	if strings.Contains(user, "B-3001") {
		return "Done: your order has been refunded."
	}
	return "Sorry about that. Which order is it?"
}

func TestConversationRemoteHTTP(t *testing.T) {
	mcp, path := startSimuser(t)
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		conv, _ := body["conversation"].(map[string]any)
		msg, _ := conv["message"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"answer": chatReply(msg), "usage": map[string]any{"input_tokens": 10, "output_tokens": 5}})
	}))
	defer srv.Close()
	env := convEnv(mcp)
	env["BENCH_REMOTE_URL"] = srv.URL + "/chat"
	env["BENCH_REMOTE_SESSION_ID"] = "conv-1"
	env["BENCH_REMOTE_OUTPUT"] = schemas.OutputState
	env["BENCH_REMOTE_TIMEOUT_S"] = "30"
	_, out, err := runPy(t, "remotehttp.py", env)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	checkRefundChat(t, readTranscript(t, path), "Sorry about that. Which order is it?", "Done: your order has been refunded.")
	if len(bodies) != 2 {
		t.Fatalf("%d requests, want 2", len(bodies))
	}
	second, _ := bodies[1]["conversation"].(map[string]any)
	msgs, _ := second["messages"].([]any)
	if bodies[1]["session_id"] != "conv-1" || bodies[1]["output"] != "conversation" || second["turn"] != float64(2) || len(msgs) != 4 {
		t.Fatalf("second request %v", bodies[1])
	}
	if !strings.Contains(out, `"ended_by": "user_stop"`) || !strings.Contains(out, `"output_tokens": 10`) {
		t.Fatalf("report missing the conversation or summed usage:\n%s", out)
	}
}

func TestConversationRemoteHTTPServerHistory(t *testing.T) {
	mcp, path := startSimuser(t)
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen = append(seen, body)
		conv, _ := body["conversation"].(map[string]any)
		msg, _ := conv["message"].(string)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, chatReply(msg))
	}))
	defer srv.Close()
	env := convEnv(mcp)
	env["BENCH_CONV_HISTORY"] = "server"
	env["BENCH_REMOTE_URL"] = srv.URL
	env["BENCH_REMOTE_OUTPUT"] = schemas.OutputState
	if _, out, err := runPy(t, "remotehttp.py", env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	checkRefundChat(t, readTranscript(t, path), "Sorry about that. Which order is it?", "Done: your order has been refunded.")
	for _, b := range seen {
		if conv, _ := b["conversation"].(map[string]any); conv["messages"] != nil {
			t.Fatal("history server sent the whole chat")
		}
	}
}

func TestConversationOpenAIAgent(t *testing.T) {
	mcp, path := startSimuser(t)
	var reqs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		reqs = append(reqs, body)
		msgs, _ := body["messages"].([]any)
		last, _ := msgs[len(msgs)-1].(map[string]any)
		text, _ := last["content"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": chatReply(text)}}},
			"usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 3}})
	}))
	defer srv.Close()
	env := convEnv(mcp)
	servers, _ := json.Marshal(map[string]any{"customer": map[string]string{"url": mcp}})
	env["BENCH_MCP_SERVERS"] = string(servers) // the customer server lists respond_to_user; the endpoint must never get it
	env["BENCH_OPENAI_URL"] = srv.URL + "/v1/chat/completions"
	env["BENCH_OPENAI_MODEL"] = "support-bot"
	env["BENCH_OPENAI_OUTPUT"] = schemas.OutputState
	if _, out, err := runPy(t, "openaiagent.py", env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	checkRefundChat(t, readTranscript(t, path), "Sorry about that. Which order is it?", "Done: your order has been refunded.")
	if len(reqs) != 2 {
		t.Fatalf("%d requests", len(reqs))
	}
	msgs, _ := reqs[1]["messages"].([]any)
	sys, _ := msgs[0].(map[string]any)
	if sys["role"] != "system" || !strings.Contains(sys["content"].(string), "Refund damaged orders") || len(msgs) != 5 {
		t.Fatalf("second request messages %v", msgs)
	}
	if _, ok := reqs[0]["tools"]; ok {
		t.Fatal("respond_to_user was offered to the endpoint")
	}
}

func TestConversationA2A(t *testing.T) {
	mcp, path := startSimuser(t)
	var msgs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		m, _ := rpc.Params["message"].(map[string]any)
		msgs = append(msgs, m)
		parts, _ := m["parts"].([]any)
		p0, _ := parts[0].(map[string]any)
		reply := chatReply(p0["text"].(string))
		state := "TASK_STATE_INPUT_REQUIRED"
		if strings.Contains(reply, "refunded") {
			state = "TASK_STATE_COMPLETED"
		}
		task := map[string]any{"id": "task-1", "contextId": "ctx-1", "status": map[string]any{"state": state,
			"message": map[string]any{"role": "ROLE_AGENT", "parts": []any{map[string]any{"text": reply}}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"task": task}})
	}))
	defer srv.Close()
	env := convEnv(mcp)
	env["BENCH_A2A_ENDPOINT"] = srv.URL + "/agents/a2a/support/json-rpc"
	env["BENCH_A2A_OUTPUT"] = schemas.OutputState
	if _, out, err := runPy(t, "a2a.py", env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	checkRefundChat(t, readTranscript(t, path), "Sorry about that. Which order is it?", "Done: your order has been refunded.")
	if len(msgs) != 2 || msgs[0]["contextId"] != nil || msgs[1]["contextId"] != "ctx-1" || msgs[1]["taskId"] != "task-1" {
		t.Fatalf("messages %v", msgs)
	}
}

// fakePull is a Heimdall pull endpoint for remote_http repo runs: it keeps
// the uploaded tarball and serves it back.
type fakePull struct {
	repo []byte
	auth string
}

func (f *fakePull) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/repos"):
		f.auth = r.Header.Get("Authorization")
		f.repo, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"url": "http://" + r.Host + "/repo.tar.gz?sig=x", "sha256": "abc", "bytes": len(f.repo)})
	case r.URL.Path == "/repo.tar.gz":
		_, _ = w.Write(f.repo)
	default:
		http.NotFound(w, r)
	}
}

func TestRemoteHTTPRepoInPatchOut(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	pull := &fakePull{}
	ps := httptest.NewServer(pull)
	defer ps.Close()
	var sent map[string]any
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		repo, _ := sent["repo"].(map[string]any)
		resp, err := http.Get(repo["url"].(string))
		if err != nil || resp.StatusCode != 200 {
			http.Error(w, "repo download failed", 500)
			return
		}
		resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"answer": "fixed", "patch": "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-scratch\n+fixed\n"})
	}))
	defer agent.Close()
	env := map[string]string{"BENCH_REMOTE_URL": agent.URL, "BENCH_REMOTE_OUTPUT": schemas.OutputDiff, "BENCH_REMOTE_FILES_DIR": "{ws}",
		"BENCH_REPO": "tarball", "BENCH_REPO_DIR": "{ws}", "BENCH_PULL_URL": ps.URL + "/api/bench/pull/run", "BENCH_PULL_TOKEN": "hpr1.test"}
	// The launcher's workspace is a temp folder runPy makes; git apply works
	// outside a repository too.
	_, out, err := runPy(t, "remotehttp.py", env)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if pull.auth != "Bearer hpr1.test" || len(pull.repo) < 2 || pull.repo[0] != 0x1f {
		t.Fatalf("upload auth %q, %d bytes", pull.auth, len(pull.repo))
	}
	if !strings.Contains(out, `"applied": true`) {
		t.Fatalf("patch not applied:\n%s", out)
	}
	if _, ok := sent["files"]; ok {
		t.Fatal("repo mode also sent the workspace inline")
	}
}

func TestRemoteHTTPRepoRefusesUnsafePatch(t *testing.T) {
	pull := &fakePull{}
	ps := httptest.NewServer(pull)
	defer ps.Close()
	for _, patch := range []string{
		"--- a/../etc/passwd\n+++ b/../etc/passwd\n@@ -1 +1 @@\n-a\n+b\n",
		"--- /dev/null\n+++ b/.git/hooks/post-checkout\n@@ -0,0 +1 @@\n+x\n",
		"diff --git a/l b/l\nnew file mode 120000\n--- /dev/null\n+++ b/l\n@@ -0,0 +1 @@\n+/etc\n",
	} {
		agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"answer": "", "patch": patch})
		}))
		env := map[string]string{"BENCH_REMOTE_URL": agent.URL, "BENCH_REMOTE_OUTPUT": schemas.OutputDiff,
			"BENCH_REPO": "tarball", "BENCH_REPO_DIR": "{ws}", "BENCH_PULL_URL": ps.URL + "/api/bench/pull/run", "BENCH_PULL_TOKEN": "hpr1.test"}
		_, out, err := runPy(t, "remotehttp.py", env)
		agent.Close()
		if err != nil || !strings.Contains(out, `"applied": false`) || !strings.Contains(out, "refused") {
			t.Fatalf("unsafe patch %q not refused (err %v):\n%s", patch, err, out)
		}
	}
}

// conversationRC is a run context with a helpdesk and a simulated customer.
func conversationRC(t *testing.T, agent *schemas.AgentConfig) (*schemas.RunContext, CommandConfig) {
	rc, cfg := fixture(t, agent)
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
	rc.Services = []schemas.ServiceHandle{
		{Spec: schemas.ServiceSpec{ID: "helpdesk", Role: schemas.ServiceRoleMCP, MCP: &schemas.MCPEndpoint{Path: "/mcp"}}, URL: "http://toolproxy:9000/s/helpdesk"},
		{Spec: schemas.ServiceSpec{ID: "customer", Role: schemas.ServiceRoleMCP, MCP: &schemas.MCPEndpoint{Path: "/mcp"}, Gateway: &schemas.ServiceGateway{}}, URL: "http://toolproxy:9000/s/customer"},
	}
	rc.Suite = &schemas.Suite{ID: "support", Simulator: &schemas.SimulatorSpec{MaxTurns: 9}}
	return rc, cfg
}

func TestConversationPlans(t *testing.T) {
	conv := &schemas.ConversationSpec{Driver: schemas.ConversationDriverBench}
	t.Run("remote_http", func(t *testing.T) {
		agent := remoteAgent("")
		agent.Remote.Tools, agent.Remote.Conversation = schemas.RemoteToolsExpose, conv
		rc, cfg := conversationRC(t, agent)
		t.Setenv("HD_REMOTE_TEST_TOKEN", testRemoteToken)
		rc.RemoteTools = &schemas.RemoteToolsAccess{RunID: rc.Run.RunID, BaseURL: "https://gate.example.com", Token: "gate-token",
			Services: []string{"customer", "helpdesk"}, MCP: []string{"customer", "helpdesk"}, MCPPaths: map[string]string{"customer": "/mcp", "helpdesk": "/mcp"}}
		plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.Env["BENCH_CONV_USERS"], "toolproxy:9000/s/customer/mcp") || strings.Contains(plan.Env["BENCH_CONV_USERS"], "helpdesk") {
			t.Fatalf("users %q", plan.Env["BENCH_CONV_USERS"])
		}
		if plan.Env["BENCH_CONV_MAX_TURNS"] != "9" || plan.Files[convHelperFile] == "" {
			t.Fatalf("env %v", plan.Env)
		}
		if strings.Contains(plan.Env["BENCH_REMOTE_TOOLS"], "customer") || !strings.Contains(plan.Env["BENCH_REMOTE_TOOLS"], "helpdesk") {
			t.Fatalf("the agent was sent the customer: %s", plan.Env["BENCH_REMOTE_TOOLS"])
		}
	})
	t.Run("openai_agent", func(t *testing.T) {
		agent := &schemas.AgentConfig{ID: "bot", Adapter: AdapterOpenAIAgent, Image: "img", Budgets: schemas.Budgets{WallS: 300},
			OpenAI: &schemas.OpenAIAgent{URL: "https://bot.example.com/v1", Conversation: conv}}
		rc, cfg := conversationRC(t, agent)
		plan, err := newOpenAIAgentAdapter(cfg).Plan(context.Background(), rc)
		if err != nil || plan.Env["BENCH_CONV_GREETING"] != schemas.DefaultConversationGreeting {
			t.Fatalf("plan %v %v", plan, err)
		}
	})
	t.Run("no simulated user runs once", func(t *testing.T) {
		agent := remoteAgent("")
		agent.Remote.Conversation = conv
		rc, cfg := remoteFixture(t, agent)
		plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
		if err != nil || plan.Env["BENCH_CONV_USERS"] != "" {
			t.Fatalf("plan %v %v", plan, err)
		}
	})
	t.Run("bad block", func(t *testing.T) {
		if err := utils.CheckConversation(&schemas.ConversationSpec{Driver: "user"}, "remote"); err == nil {
			t.Fatal("bad driver accepted")
		}
		if err := utils.CheckConversation(&schemas.ConversationSpec{MaxTurns: 101}, "remote"); err == nil {
			t.Fatal("too many turns accepted")
		}
	})
	t.Run("history follows the protocol", func(t *testing.T) {
		if err := utils.CheckConversation(&schemas.ConversationSpec{Driver: "bench", History: "client"}, "a2a"); err == nil || !strings.Contains(err.Error(), "context") {
			t.Fatalf("a2a history client: %v", err)
		}
		if err := utils.CheckConversation(&schemas.ConversationSpec{Driver: "bench", History: "server"}, "openai"); err == nil || !strings.Contains(err.Error(), "keeps no state") {
			t.Fatalf("openai history server: %v", err)
		}
		for _, block := range []string{"remote", "pull"} {
			for _, h := range []string{"", "client", "server"} {
				if err := utils.CheckConversation(&schemas.ConversationSpec{Driver: "bench", History: h}, block); err != nil {
					t.Fatalf("%s history %q: %v", block, h, err)
				}
			}
		}
	})
}

func TestRemoteHTTPRepoPlan(t *testing.T) {
	agent := remoteAgent("")
	agent.Remote.Repo = schemas.RepoTarball
	rc, cfg := remoteFixture(t, agent)
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputDiff}
	if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "pull.url") {
		t.Fatalf("no pull URL: %v", err)
	}
	cfg.PullURL, cfg.OrgID = "https://heimdall.example.com/api/bench/pull/run", "org_a"
	plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Egress) != 2 || plan.Egress[1] != "heimdall.example.com:443" || plan.Env["BENCH_PULL_CONNECT"] != "bench-eg-r_p20fj3s8wa-2:9300" {
		t.Fatalf("egress %v env %v", plan.Egress, plan.Env)
	}
	c, err := pullagent.DefaultSigner().VerifyRun(plan.Env["BENCH_PULL_TOKEN"], time.Now())
	if err != nil || c.Org != "org_a" || c.Run != rc.Run.RunID || c.Agent != agent.ID {
		t.Fatalf("token claims %+v %v", c, err)
	}
	agent.Remote.Repo = ""
	if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "remote.repo tarball") {
		t.Fatalf("diff without repo mode: %v", err)
	}
}

func TestPullPlan(t *testing.T) {
	agent := &schemas.AgentConfig{ID: "support-bot", Adapter: AdapterPull, Image: "img", Budgets: schemas.Budgets{WallS: 300},
		Pull: &schemas.PullAgent{Conversation: &schemas.ConversationSpec{Driver: schemas.ConversationDriverBench}}}
	rc, cfg := conversationRC(t, agent)
	cfg.PullURL, cfg.OrgID = "https://heimdall.example.com/api/bench/pull/run", "org_a"
	plan, err := newPullAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Argv, " ") != "python3 -I "+pullLauncherFile || len(plan.Egress) != 1 || plan.Egress[0] != "heimdall.example.com:443" {
		t.Fatalf("argv %v egress %v", plan.Argv, plan.Egress)
	}
	for _, k := range []string{"BENCH_PULL_URL", "BENCH_PULL_TOKEN", "BENCH_PULL_CONNECT", utils.EnvMCPServers, "BENCH_CONV_USERS"} {
		if plan.Env[k] == "" {
			t.Fatalf("env lacks %s: %v", k, plan.Env)
		}
	}
	if plan.Env["BENCH_PULL_AGENT"] != "support-bot" || plan.Files[pullLauncherFile] == "" || plan.Files[convHelperFile] == "" {
		t.Fatalf("env %v", plan.Env)
	}
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputDiff}
	if _, err := newPullAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "pull.repo tarball") {
		t.Fatalf("diff without repo: %v", err)
	}
	agent.Pull.Repo = schemas.RepoTarball
	plan, err = newPullAdapter(cfg).Plan(context.Background(), rc)
	if err != nil || plan.Env["BENCH_REPO"] != "tarball" || len(plan.Egress) != 1 {
		t.Fatalf("repo plan %v %v", plan, err)
	}
	cfg.PullURL = ""
	if _, err := newPullAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "pull.url") {
		t.Fatalf("no pull URL: %v", err)
	}
	agent.Pull.Name = "bad name!"
	if err := utils.CheckPullAgent(agent); err == nil {
		t.Fatal("bad name accepted")
	}
}
