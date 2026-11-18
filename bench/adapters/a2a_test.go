package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func a2aAgent(spec *schemas.A2AAgent) *schemas.AgentConfig {
	return &schemas.AgentConfig{
		ID: "a2a-agent", Adapter: AdapterA2A, Image: "bench/helper:1", Model: "claude-test",
		Budgets: schemas.Budgets{WallS: 300, CPUs: 1, MemGB: 1}, A2A: spec,
	}
}

func a2aFixture(t *testing.T, spec *schemas.A2AAgent) (*schemas.RunContext, CommandConfig) {
	t.Helper()
	rc, cfg := fixture(t, a2aAgent(spec))
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
	rc.Key = &schemas.RunKey{ID: "vk-1", Value: "run-key-secret-0123456789"}
	rc.A2A = &schemas.A2ARun{Entry: "hb-r-1-lead", Members: map[string]string{"hb-r-1-lead": "lead", "hb-r-1-writer": "writer"}}
	return rc, cfg
}

func TestA2APlan(t *testing.T) {
	rc, cfg := a2aFixture(t, &schemas.A2AAgent{CardURL: "http://agents:9000/x/.well-known/agent-card.json", Streaming: true, Artifacts: "inline"})
	plan, err := newA2AAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Argv, " ") != "python3 -I "+a2aLauncherFile || plan.Network != testNetwork || len(plan.Egress) != 0 {
		t.Fatalf("argv %v network %q egress %v", plan.Argv, plan.Network, plan.Egress)
	}
	base := strings.TrimRight(cfg.GatewayAgentURL, "/")
	want := map[string]string{
		"BENCH_A2A_ENDPOINT":  base + "/agents/a2a/hb-r-1-lead/json-rpc",
		"BENCH_A2A_KEY":       "run-key-secret-0123456789",
		"BENCH_A2A_STREAMING": "1",
		"BENCH_A2A_ARTIFACTS": "inline",
		"BENCH_A2A_OUTPUT":    schemas.OutputAnswer,
		"BENCH_A2A_TEAM":      `{"writer":"` + base + `/agents/a2a/hb-r-1-writer/json-rpc"}`,
	}
	for k, v := range want {
		if plan.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, plan.Env[k], v)
		}
	}
	if plan.Files[a2aLauncherFile] == "" || plan.Files[InstructionFile] == "" {
		t.Fatal("the plan must carry the launcher and the instruction")
	}
	if utils.HasUnredactedSecrets(a2aLauncherSource) {
		t.Fatal("the launcher source lands in plan.json, which the seal scans for secrets: it must not look like one")
	}
	for _, a := range plan.Argv {
		if strings.Contains(a, "run-key-secret") {
			t.Fatal("the run key must never be in argv")
		}
	}
}

func TestA2APlanRefuses(t *testing.T) {
	cases := map[string]func(rc *schemas.RunContext){
		"diff task":       func(rc *schemas.RunContext) { rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputDiff} },
		"artifact task":   func(rc *schemas.RunContext) { rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact} },
		"no registration": func(rc *schemas.RunContext) { rc.A2A = nil },
		"no key":          func(rc *schemas.RunContext) { rc.Key = nil },
		"no a2a block":    func(rc *schemas.RunContext) { rc.Agent.A2A = nil },
		"both targets":    func(rc *schemas.RunContext) { rc.Agent.A2A.Registration = "existing" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rc, cfg := a2aFixture(t, &schemas.A2AAgent{CardURL: "http://agents:9000/x/.well-known/agent-card.json"})
			mutate(rc)
			if _, err := newA2AAdapter(cfg).Plan(context.Background(), rc); err == nil {
				t.Fatal("want a refusal")
			}
		})
	}
}

// fakeA2A is a gateway endpoint speaking the A2A v1 JSON-RPC binding.
type fakeA2A struct {
	mu      sync.Mutex
	methods []string
	keys    []string
	// sendResult is the SendMessage result; stream is the events of
	// SendStreamingMessage; getStates are GetTask's states in order.
	sendResult string
	stream     []string
	getStates  []string
	rpcError   bool
}

func (f *fakeA2A) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(raw, &req)
	f.mu.Lock()
	f.methods = append(f.methods, req.Method)
	f.keys = append(f.keys, r.Header.Get("x-bf-vk"))
	f.mu.Unlock()
	reply := func(result string) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":%s}`, req.ID, result)
	}
	if f.rpcError {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"error":{"code":-31403,"message":"not allowed"}}`, req.ID)
		return
	}
	switch req.Method {
	case "SendMessage":
		reply(f.sendResult)
	case "SendStreamingMessage", "SubscribeToTask":
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range f.stream {
			fmt.Fprintf(w, "id: x\ndata: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":%s}\n\n", req.ID, e)
		}
	case "GetTask":
		f.mu.Lock()
		state := "TASK_STATE_COMPLETED"
		if len(f.getStates) > 0 {
			state, f.getStates = f.getStates[0], f.getStates[1:]
		}
		f.mu.Unlock()
		reply(fmt.Sprintf(`{"id":"t1","contextId":"c1","status":{"state":%q},"artifacts":[{"artifactId":"a","parts":[{"text":"30 days"}]}]}`, state))
	default:
		reply(`{}`)
	}
}

// runA2ALauncher runs a2a.py on the host against the fake gateway.
func runA2ALauncher(t *testing.T, f *fakeA2A, env map[string]string) (answer string, out string, ws string, err error) {
	t.Helper()
	py, lerr := exec.LookPath("python3")
	if lerr != nil {
		t.Skip("python3 not on PATH")
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	script := filepath.Join(dir, "a2a.py")
	instruction := filepath.Join(dir, "instruction.md")
	ws = filepath.Join(dir, "ws")
	for p, content := range map[string]string{script: a2aLauncherSource, instruction: "What is the refund window?", filepath.Join(ws, "docs/handbook.md"): "30 days"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ans := filepath.Join(ws, "answer.txt")
	cmd := exec.Command(py, "-I", script)
	cmd.Env = []string{"BENCH_A2A_ENDPOINT=" + srv.URL + "/agents/a2a/hb-r-1-lead/json-rpc", "BENCH_A2A_KEY=run-key", "BENCH_A2A_INSTRUCTION=" + instruction,
		"BENCH_ANSWER_PATH=" + ans, "BENCH_A2A_FILES_DIR=" + ws, "BENCH_A2A_TIMEOUT_S=20"}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	b, err := cmd.CombinedOutput()
	got, _ := os.ReadFile(ans)
	return string(got), string(b), ws, err
}

func lastReport(t *testing.T, out string) schemas.A2ALauncherReport {
	t.Helper()
	var rep schemas.A2ALauncherReport
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, `"a2a_report"`) {
			if err := json.Unmarshal([]byte(line), &rep); err != nil {
				t.Fatalf("bad report %q: %v", line, err)
			}
		}
	}
	return rep
}

const a2aDone = `{"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"artifactId":"a","parts":[{"text":"30 days"}]}]}}`

func TestA2ALauncherSendMessage(t *testing.T) {
	f := &fakeA2A{sendResult: a2aDone}
	answer, out, _, err := runA2ALauncher(t, f, nil)
	if err != nil || answer != "30 days" {
		t.Fatalf("answer %q err %v\n%s", answer, err, out)
	}
	rep := lastReport(t, out)
	if rep.TaskID != "t1" || rep.FinalState != "TASK_STATE_COMPLETED" || len(rep.Operations) != 1 || rep.Operations[0].TaskID != "t1" {
		t.Fatalf("report %+v", rep)
	}
	if f.keys[0] != "run-key" || strings.Contains(out, "run-key") {
		t.Fatalf("the run key must be sent as x-bf-vk and never printed; keys %v", f.keys)
	}
}

func TestA2ALauncherPolls(t *testing.T) {
	f := &fakeA2A{sendResult: `{"task":{"id":"t1","status":{"state":"TASK_STATE_WORKING"}}}`, getStates: []string{"TASK_STATE_WORKING", "TASK_STATE_COMPLETED"}}
	answer, out, _, err := runA2ALauncher(t, f, nil)
	if err != nil || answer != "30 days" || lastReport(t, out).Polls != 2 {
		t.Fatalf("answer %q err %v\n%s", answer, err, out)
	}
}

func TestA2ALauncherStreamArtifacts(t *testing.T) {
	f := &fakeA2A{stream: []string{
		`{"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_WORKING"}}}`,
		`{"artifactUpdate":{"taskId":"t1","artifact":{"artifactId":"a","parts":[{"text":"30 "}]}}}`,
		`{"artifactUpdate":{"taskId":"t1","append":true,"artifact":{"artifactId":"a","parts":[{"text":"days"}]}}}`,
		`{"artifactUpdate":{"taskId":"t1","artifact":{"artifactId":"f","parts":[{"text":"a,b\n","filename":"out/report.csv"}]}}}`,
		`{"artifactUpdate":{"taskId":"t1","artifact":{"artifactId":"g","parts":[{"text":"x","filename":"../escape.txt"}]}}}`,
		`{"statusUpdate":{"taskId":"t1","status":{"state":"TASK_STATE_COMPLETED"}}}`,
	}}
	answer, out, ws, err := runA2ALauncher(t, f, map[string]string{"BENCH_A2A_STREAMING": "1", "BENCH_A2A_ARTIFACTS": "inline"})
	if err != nil || answer != "30 days" {
		t.Fatalf("answer %q err %v\n%s", answer, err, out)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "out/report.csv")); err != nil || string(b) != "a,b\n" {
		t.Fatalf("artifact file %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ws), "escape.txt")); err == nil {
		t.Fatal("an artifact must never leave the workspace")
	}
	rep := lastReport(t, out)
	if rep.StreamEnded != "terminal" || rep.Recovered != "" || len(rep.Artifacts) != 1 {
		t.Fatalf("report %+v", rep)
	}
}

func TestA2ALauncherRecoversEarlyStream(t *testing.T) {
	stream := []string{`{"task":{"id":"t1","status":{"state":"TASK_STATE_WORKING"}}}`, `{"statusUpdate":{"taskId":"t1","status":{"state":"TASK_STATE_WORKING"}}}`}
	t.Run("agent ends the stream", func(t *testing.T) {
		f := &fakeA2A{stream: stream}
		answer, out, _, err := runA2ALauncher(t, f, map[string]string{"BENCH_A2A_STREAMING": "1"})
		rep := lastReport(t, out)
		if err != nil || answer != "30 days" || rep.StreamEnded != "early" || rep.Recovered != "get_task" {
			t.Fatalf("answer %q err %v report %+v\n%s", answer, err, rep, out)
		}
	})
	t.Run("probe drops the stream", func(t *testing.T) {
		f := &fakeA2A{stream: append(stream, `{"statusUpdate":{"taskId":"t1","status":{"state":"TASK_STATE_COMPLETED"}}}`)}
		answer, out, _, err := runA2ALauncher(t, f, map[string]string{"BENCH_A2A_STREAMING": "1", "BENCH_A2A_FAULT": `{"stream":"drop","after_events":1}`})
		rep := lastReport(t, out)
		if err != nil || answer != "30 days" || rep.Fault == nil || !rep.Fault.Fired || rep.Recovered != "get_task" {
			t.Fatalf("answer %q err %v report %+v\n%s", answer, err, rep, out)
		}
		if strings.Join(f.methods, ",") != "SendStreamingMessage,GetTask" {
			t.Fatalf("methods %v", f.methods)
		}
	})
	t.Run("still working after GetTask subscribes", func(t *testing.T) {
		f := &fakeA2A{stream: stream, getStates: []string{"TASK_STATE_WORKING"}}
		_, out, _, _ := runA2ALauncher(t, f, map[string]string{"BENCH_A2A_STREAMING": "1"})
		if rep := lastReport(t, out); rep.Recovered != "subscribe" {
			t.Fatalf("report %+v\n%s", rep, out)
		}
	})
}

func TestA2ALauncherTaskNotCompleted(t *testing.T) {
	cases := map[string]*fakeA2A{
		"failed task":    {sendResult: `{"task":{"id":"t1","status":{"state":"TASK_STATE_FAILED","message":{"parts":[{"text":"broken"}]}}}}`},
		"input required": {sendResult: `{"task":{"id":"t1","status":{"state":"TASK_STATE_INPUT_REQUIRED"}}}`},
		"no text":        {sendResult: `{"task":{"id":"t1","status":{"state":"TASK_STATE_COMPLETED"}}}`},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			answer, out, ws, err := runA2ALauncher(t, f, nil)
			if err != nil {
				t.Fatalf("a task that ends without completing is the agent's result, not a crash: %v\n%s", err, out)
			}
			if b, rerr := os.ReadFile(filepath.Join(ws, "answer.txt")); rerr != nil || len(b) != 0 || answer != "" {
				t.Fatalf("want an empty answer file, got %q %v", b, rerr)
			}
			if rep := lastReport(t, out); rep.Error == "" || rep.FinalState == "" {
				t.Fatalf("the report must carry the state and the reason: %+v", rep)
			}
		})
	}
}

func TestA2ALauncherFailures(t *testing.T) {
	cases := map[string]*fakeA2A{
		"json-rpc error": {rpcError: true},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			answer, out, _, err := runA2ALauncher(t, f, nil)
			if err == nil || answer != "" {
				t.Fatalf("want a failed run, got answer %q\n%s", answer, out)
			}
			if rep := lastReport(t, out); rep.Error == "" {
				t.Fatalf("the report must carry the reason: %+v", rep)
			}
		})
	}
}
