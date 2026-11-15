package adapters

import (
	"context"
	"encoding/json"
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
)

func scriptedAgent(kind string) *schemas.AgentConfig {
	return &schemas.AgentConfig{
		ID:       kind,
		Adapter:  AdapterScripted,
		Image:    "bench/coding-core-base:1",
		Model:    "claude-sonnet-4-5",
		Budgets:  schemas.Budgets{WallS: 900, CPUs: 1, MemGB: 1},
		Scripted: &schemas.ScriptedSpec{Kind: kind},
	}
}

func TestScriptedPlans(t *testing.T) {
	tests := []struct {
		kind      string
		argv      []string
		network   string
		files     map[string]string
		keyInEnv  bool
		noEnvKeys bool
	}{
		{kind: ScriptedNoop, argv: []string{"true"}, network: "none", noEnvKeys: true},
		{kind: ScriptedRefuse, argv: []string{"echo", RefusalText}, network: "none", noEnvKeys: true},
		{kind: ScriptedOracle, argv: gitApply, network: "none", files: map[string]string{referencePatchFile: testPatch}, noEnvKeys: true},
		{kind: ScriptedExploit, argv: []string{"sh", exploitFile}, network: "none", files: map[string]string{exploitFile: testExploit}, noEnvKeys: true},
		{kind: ScriptedToy, argv: []string{"python3", "-I", toyAgentFile}, network: testNetwork, keyInEnv: true,
			files: map[string]string{toyAgentFile: toyAgentSource, referencePatchFile: testPatch, InstructionFile: testInstruction}},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			rc, cfg := fixture(t, scriptedAgent(tt.kind))
			plan, err := newScriptedAdapter(cfg).Plan(context.Background(), rc)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(plan.Argv, " ") != strings.Join(tt.argv, " ") {
				t.Errorf("argv = %q, want %q", plan.Argv, tt.argv)
			}
			if plan.Network != tt.network {
				t.Errorf("network = %q, want %q", plan.Network, tt.network)
			}
			if len(plan.Files) != len(tt.files) {
				t.Errorf("files = %d, want %d", len(plan.Files), len(tt.files))
			}
			for p, want := range tt.files {
				if plan.Files[p] != want {
					t.Errorf("file %s content mismatch", p)
				}
			}
			if tt.noEnvKeys && len(plan.Env) != 0 {
				t.Errorf("control %s got env %v", tt.kind, plan.Env)
			}
			if tt.keyInEnv && plan.Env["BENCH_RUN_KEY"] != testKey {
				t.Errorf("toy key not in env")
			}
			if plan.WallS != 600 || plan.User != "1000" || plan.Workdir != Workspace || plan.Image != "bench/coding-core-base:1" {
				t.Errorf("plan basics wrong: %+v", plan)
			}
			stripped := *plan
			stripped.Env = nil
			b, err := json.Marshal(stripped)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), testKey) {
				t.Errorf("key outside env")
			}
		})
	}
}

func TestScriptedPlanErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(rc *schemas.RunContext, cfg *CommandConfig)
		want   string
	}{
		{"unknown kind", func(rc *schemas.RunContext, _ *CommandConfig) { rc.Agent.Scripted.Kind = "magic" }, "unknown scripted kind"},
		{"no scripted block", func(rc *schemas.RunContext, _ *CommandConfig) { rc.Agent.Scripted = nil }, "no scripted block"},
		{"oracle without patch", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Scripted.Kind = ScriptedOracle
			rc.Task.Controls.Reference = ""
		}, "empty file name"},
		{"toy without key", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Scripted.Kind = ScriptedToy
			rc.Key = nil
		}, "no run key"},
		{"toy without gateway", func(rc *schemas.RunContext, cfg *CommandConfig) {
			rc.Agent.Scripted.Kind = ScriptedToy
			cfg.GatewayAgentURL = ""
		}, "gateway agent url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc, cfg := fixture(t, scriptedAgent(ScriptedNoop))
			tt.mutate(rc, &cfg)
			_, err := newScriptedAdapter(cfg).Plan(context.Background(), rc)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestToyAgentCompiles(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	src := filepath.Join(t.TempDir(), "toyagent.py")
	if err := os.WriteFile(src, []byte(toyAgentSource), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(py, "-c", "import ast, sys; ast.parse(open(sys.argv[1]).read(), sys.argv[1])", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("toyagent.py does not parse: %v\n%s", err, out)
	}
	cmd = exec.Command(py, "-m", "py_compile", src)
	cmd.Env = append(os.Environ(), "PYTHONPYCACHEPREFIX="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("py_compile toyagent.py: %v\n%s", err, out)
	}
}

// TestToyAgentThroughParser runs the toy against a local fake Messages API and
// feeds its stdout to the claudecode parser: the toy is only useful if the
// parser reads it like Claude Code.
func TestToyAgentThroughParser(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tests := []struct {
		name      string
		status    int
		wantCalls int
		wantClaim bool
		wantExit  int
	}{
		{"healthy model", http.StatusOK, 3, true, 0},
		{"model outage", 529, 1, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read toy request: %v", err)
				}
				var req map[string]any
				if err := json.Unmarshal(body, &req); err != nil {
					t.Errorf("toy sent invalid json: %v", err)
				}
				mu.Lock()
				seen = append(seen, req)
				n := len(seen)
				mu.Unlock()
				if r.URL.Path != "/anthropic/v1/messages" || r.Header.Get("x-api-key") != testKey ||
					r.Header.Get("x-bf-session-id") != "r_p20fj3s8wa" {
					t.Errorf("bad request: path %s key %q session %q", r.URL.Path, r.Header.Get("x-api-key"), r.Header.Get("x-bf-session-id"))
				}
				if tt.status != http.StatusOK {
					w.WriteHeader(tt.status)
					reply(t, w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
					return
				}
				w.Header().Set("content-type", "application/json")
				reply(t, w, fakeMessage(n))
			}))
			defer srv.Close()

			ws := t.TempDir()
			if err := os.WriteFile(filepath.Join(ws, "x.py"), []byte("a = 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			files := map[string]string{
				"toyagent.py":    toyAgentSource,
				"instruction.md": testInstruction,
				"patch.diff":     "diff --git a/x.py b/x.py\n--- a/x.py\n+++ b/x.py\n@@ -1 +1 @@\n-a = 1\n+a = 2\n",
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(py, "-I", filepath.Join(dir, "toyagent.py"))
			cmd.Dir = ws
			cmd.Env = append(os.Environ(),
				"BENCH_GATEWAY_URL="+srv.URL, "BENCH_RUN_KEY="+testKey, "BENCH_MODEL=claude-sonnet-4-5",
				"BENCH_SESSION=r_p20fj3s8wa", "BENCH_INSTRUCTION="+filepath.Join(dir, "instruction.md"),
				"BENCH_PATCH="+filepath.Join(dir, "patch.diff"), "BENCH_WORKSPACE="+ws)
			out, err := cmd.Output()
			exit := 0
			if ee, ok := err.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if exit != tt.wantExit {
				t.Fatalf("exit = %d, want %d; stdout:\n%s", exit, tt.wantExit, out)
			}
			if len(seen) != tt.wantCalls {
				t.Fatalf("model calls = %d, want %d", len(seen), tt.wantCalls)
			}
			if _, ok := seen[0]["tools"]; !ok {
				t.Errorf("first call has no tool definition")
			}

			rc, _ := fixture(t, scriptedAgent(ScriptedToy))
			res, err := newClaudeCodeParser().Parse(context.Background(), rc, out)
			if err != nil {
				t.Fatal(err)
			}
			if res.Claim == nil || res.Claim.Made != tt.wantClaim {
				t.Fatalf("claim = %+v, want made=%v; stdout:\n%s", res.Claim, tt.wantClaim, out)
			}
			counts := countEvents(res.Events)
			if tt.status == http.StatusOK {
				if counts[schemas.EventModelResponse] != 3 || counts[schemas.EventToolCall] != 1 || counts[schemas.EventToolResult] != 1 {
					t.Errorf("event counts = %v", counts)
				}
				if res.Events[0].Tokens == nil || res.Events[0].Tokens.Fresh != 10 || res.Claim.Matched != "all tests pass" {
					t.Errorf("toy turn tokens %+v, claim %+v", res.Events[0].Tokens, res.Claim)
				}
				b, err := os.ReadFile(filepath.Join(ws, "x.py"))
				if err != nil || string(b) != "a = 2\n" {
					t.Errorf("patch not applied: %q %v", b, err)
				}
			} else if counts[schemas.EventError] < 1 || counts[schemas.EventModelResponse] != 0 {
				t.Errorf("event counts = %v, want an error and no model turn", counts)
			}
		})
	}
}

func reply(t *testing.T, w io.Writer, body string) {
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write fake response: %v", err)
	}
}

// fakeMessage returns a Messages API response; the first asks for the tool.
func fakeMessage(n int) string {
	content := `[{"type":"text","text":"Noted."}]`
	stop := "end_turn"
	if n == 1 {
		content = `[{"type":"text","text":"Let me look."},{"type":"tool_use","id":"toolu_toy1","name":"read_file","input":{"path":"x.py"}}]`
		stop = "tool_use"
	}
	return `{"id":"msg_toy` + string(rune('0'+n)) + `","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":` +
		content + `,"stop_reason":"` + stop + `","usage":{"input_tokens":10,"output_tokens":5}}`
}

func countEvents(events []schemas.Event) map[schemas.EventType]int {
	out := map[schemas.EventType]int{}
	for _, e := range events {
		out[e.Type]++
	}
	return out
}

func TestScriptedOracleControlKinds(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		content  string
		output   *schemas.OutputSpec
		argv     []string
		fileName string
	}{
		{"script reference", "controls/reference.sh", "#!/bin/sh\necho ok > /workspace/x\n", nil,
			[]string{"sh", referenceScriptFile}, referenceScriptFile},
		{"answer reference", "controls/answer.txt", "42\n", &schemas.OutputSpec{Kind: schemas.OutputAnswer, AnswerPath: "out/a.txt"},
			[]string{"sh", "-c", writeAnswer, "write-answer", "/work/out/a.txt", referenceAnswerFile}, referenceAnswerFile},
		{"artifact task runs its reference script", "controls/reference.sh", "#!/bin/sh\nmkdir -p out && echo a,b > out/r.csv\n",
			&schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "report", Path: "out/*.csv", Required: true}}},
			[]string{"sh", referenceScriptFile}, referenceScriptFile},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rc, cfg := fixture(t, scriptedAgent(ScriptedOracle))
			rc.Suite = &schemas.Suite{Workdir: "/work"}
			rc.Task.Controls.Reference = tc.ref
			rc.Task.Output = tc.output
			p := filepath.Join(rc.Task.Dir, filepath.FromSlash(tc.ref))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			plan, err := newScriptedAdapter(cfg).Plan(context.Background(), rc)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(plan.Argv, "\x00") != strings.Join(tc.argv, "\x00") {
				t.Errorf("argv = %q, want %q", plan.Argv, tc.argv)
			}
			if plan.Files[tc.fileName] != tc.content || len(plan.Files) != 1 || plan.Network != "none" {
				t.Errorf("files %v network %s", plan.Files, plan.Network)
			}
		})
	}
}

// TestScriptedWriteAnswer runs the answer oracle's shell line on the host:
// it creates the answer folder and copies the reference answer there.
func TestScriptedWriteAnswer(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "ref")
	if err := os.WriteFile(src, []byte("42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out", "a.txt")
	if out, err := exec.Command("sh", "-c", writeAnswer, "write-answer", dst, src).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "42\n" {
		t.Fatalf("answer %q %v", b, err)
	}
}

// TestScriptedNoopArtifactTask: noop leaves an artifact task's files
// unwritten, so its required artifacts are missing and the run fails.
func TestScriptedNoopArtifactTask(t *testing.T) {
	rc, cfg := fixture(t, scriptedAgent(ScriptedNoop))
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "report", Path: "out/*.csv", Required: true}}}
	plan, err := newScriptedAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Argv, " ") != "true" || len(plan.Files) != 0 || plan.Network != "none" {
		t.Fatalf("noop plan %v %v %s", plan.Argv, plan.Files, plan.Network)
	}
}
