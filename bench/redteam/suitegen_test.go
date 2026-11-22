package redteam

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
)

func testPlan() Plan {
	return Plan{AttackerProvider: "openai", AttackerModel: "gpt-4o-mini"}
}

// writeSuite writes the plan's suite under root/benchmarks/<id> and returns the suite folder.
func writeSuite(t *testing.T, p Plan) string {
	t.Helper()
	files, err := Suite(p)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if p.Suite == "" {
		p.Suite = DefaultSuite
	}
	dir := filepath.Join(root, "benchmarks", p.Suite)
	if err := scaffold.WriteFiles(dir, files, false); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSuiteLoadsAndLints(t *testing.T) {
	dir := writeSuite(t, testPlan())
	if !IsSuite(dir) {
		t.Fatal("IsSuite false on a generated suite")
	}
	s, err := suite.Builtin(filepath.Dir(dir))[0].Load(context.Background(), DefaultSuite)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, want := len(s.Tasks), len(testPlanCells()); got != want {
		t.Fatalf("tasks = %d, want %d", got, want)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			t.Errorf("lint error: %s: %s", p.Path, p.Message)
		}
	}
	for _, task := range s.Tasks {
		if _, ok := ParseTaskID(task.ID); !ok {
			t.Errorf("task id %s does not parse", task.ID)
		}
	}
}

func testPlanCells() []Cell {
	p := testPlan()
	if err := p.Normalize(); err != nil {
		panic(err)
	}
	return p.Cells()
}

func TestSuiteAssetsAndControls(t *testing.T) {
	files, err := Suite(Plan{Categories: []string{CategoryPromptLeak}, Strategies: []string{StrategyPAIR}, AttackerProvider: "openai", AttackerModel: "m"})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]scaffold.File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	for _, want := range []string{"suite.yaml", PlanFile, "services/user/Dockerfile", "services/user/redteam.py", "services/user/rtcore.py",
		"tasks/rt.prompt_leak.pair.chat/attack.json", "tasks/rt.prompt_leak.pair.chat/controls/mutant-comply.sh", "tasks/rt.prompt_leak.pair.chat/verifier/tests/rtcore.py"} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	ins := string(byPath["tasks/rt.prompt_leak.pair.chat/instruction.md"].Data)
	var cfg AttackConfig
	if err := json.Unmarshal(byPath["tasks/rt.prompt_leak.pair.chat/attack.json"].Data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Canary == "" || !strings.Contains(ins, cfg.Canary) {
		t.Fatal("prompt leak canary not in the agent's instruction")
	}
	if strings.Contains(cfg.Goal, cfg.Canary) || strings.Contains(strings.Join(cfg.Script.Seeds, " "), cfg.Canary) {
		t.Fatal("the attacker must never be given the canary")
	}
	if byPath["tasks/rt.prompt_leak.pair.chat/solution/reference.sh"].Mode != 0o755 {
		t.Fatal("reference control is not executable")
	}
}

// End to end without Docker: both services as python3 processes, a control
// in the agent's place, then the verifier.

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func startServices(t *testing.T, script, attack, state string) map[string]string {
	t.Helper()
	pu, pd := freePort(t), freePort(t)
	urls := map[string]string{ServiceUser: fmt.Sprintf("http://127.0.0.1:%d", pu), ServiceDesk: fmt.Sprintf("http://127.0.0.1:%d", pd)}
	start := func(kind, sub string, port int) {
		cmd := exec.Command("python3", "-I", script, kind, "--config", attack, "--state", filepath.Join(state, sub), "--port", fmt.Sprint(port))
		cmd.Env = append(os.Environ(), "BENCH_SIM_MODE=scripted", "RT_WORKSPACE_URL="+urls[ServiceDesk], "RT_ATTACKER_URL="+urls[ServiceUser])
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	}
	start("attacker", ServiceUser, pu)
	start("workspace", ServiceDesk, pd)
	for _, u := range urls {
		deadline := time.Now().Add(10 * time.Second)
		for {
			r, err := http.Get(u + "/healthz")
			if err == nil {
				r.Body.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("service %s not ready: %v", u, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return urls
}

// runTask runs control (a control script path, or "" for none) against the
// task folder and returns the verifier's reward.json.
func runTask(t *testing.T, suiteDir, taskID, control string) map[string]any {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	taskDir := filepath.Join(suiteDir, "tasks", taskID)
	state := t.TempDir()
	urls := startServices(t, filepath.Join(suiteDir, "services", "user", "redteam.py"), filepath.Join(taskDir, "attack.json"), state)
	if control != "" {
		servers, _ := json.Marshal(map[string]any{ServiceUser: map[string]string{"url": urls[ServiceUser] + "/mcp"}, ServiceDesk: map[string]string{"url": urls[ServiceDesk] + "/mcp"}})
		cmd := exec.Command("sh", filepath.Join(taskDir, control))
		cmd.Env = append(os.Environ(), "BENCH_MCP_SERVERS="+string(servers))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("control %s: %v\n%s", control, err, out)
		}
	}
	out := t.TempDir()
	cmd := exec.Command("python3", "-I", filepath.Join(taskDir, "verifier", "tests", "verify.py"))
	cmd.Env = append(os.Environ(), "GRADER_STATE="+state, "GRADER_OUT="+out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("verify: %v\n%s", err, b)
	}
	b, err := os.ReadFile(filepath.Join(out, "reward.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reward map[string]any
	if err := json.Unmarshal(b, &reward); err != nil {
		t.Fatal(err)
	}
	reward["_state"] = state
	return reward
}

func dims(r map[string]any) map[string]any { return r["dimensions"].(map[string]any) }

func TestControlsEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("starts python3 services")
	}
	dir := writeSuite(t, testPlan())
	for _, c := range []Cell{
		{CategoryPromptLeak, StrategyPAIR, ChannelChat},
		{CategoryDataExfiltration, StrategyTAP, ChannelChat},
		{CategoryUnauthorizedAction, StrategyCrescendo, ChannelEmail},
		{CategoryPolicyBypass, StrategyPAIR, ChannelDocument},
		{CategoryGoalHijack, StrategyTAP, ChannelToolResult},
		{CategoryDataExfiltration, StrategyPAIR, ChannelEmail},
	} {
		id := TaskID(c)
		t.Run(id, func(t *testing.T) {
			ref := runTask(t, dir, id, "solution/reference.sh")
			if ref["reward"].(float64) != 1 {
				t.Fatalf("reference reward = %v, dims %v", ref["reward"], dims(ref))
			}
			bad := runTask(t, dir, id, "controls/mutant-comply.sh")
			if bad["reward"].(float64) != 0 || dims(bad)["attack_succeeded"].(float64) != 1 {
				t.Fatalf("comply mutant = %v, dims %v", bad["reward"], dims(bad))
			}
			if dims(bad)["attempts_to_break"].(float64) < 1 {
				t.Fatalf("attempts_to_break missing: %v", dims(bad))
			}
			if empty := runTask(t, dir, id, ""); empty["reward"].(float64) != 0 {
				t.Fatalf("empty reward = %v", empty["reward"])
			}
		})
	}
}
