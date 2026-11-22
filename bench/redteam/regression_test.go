package redteam

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func brokenState(t *testing.T, stateDir string) *RunState {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(stateDir, ServiceUser, StateExport))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseRunState(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRegressionReplaysExactPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("starts python3 services")
	}
	dir := writeSuite(t, testPlan())
	for _, c := range []Cell{{CategoryDataExfiltration, StrategyTAP, ChannelChat}, {CategoryUnauthorizedAction, StrategyCrescendo, ChannelEmail}} {
		id := TaskID(c)
		t.Run(id, func(t *testing.T) {
			bad := runTask(t, dir, id, "controls/mutant-comply.sh")
			state := brokenState(t, bad["_state"].(string))
			if !state.Broken {
				t.Fatal("comply run not marked broken")
			}
			orig, err := ReadAttack(filepath.Join(dir, "tasks", id))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := Regression(orig, state)
			if err != nil {
				t.Fatal(err)
			}
			if !IsRegressionTask(cfg.ID) || cfg.ReplaySHA256 != PayloadSHA256(*cfg.Replay) {
				t.Fatalf("regression id %s / sha %s", cfg.ID, cfg.ReplaySHA256)
			}
			reg := filepath.Join(t.TempDir(), "benchmarks", RegressionsSuite)
			added, err := AddRegression(reg, cfg, ReviewItem{Run: "run-1", SourceSuite: DefaultSuite, SourceTask: id})
			if err != nil || !added {
				t.Fatalf("add: %v %v", added, err)
			}
			if again, _ := AddRegression(reg, cfg, ReviewItem{Run: "run-2"}); again {
				t.Fatal("same payload added twice")
			}
			// The replay runs with no model: the python side checks the pinned sha.
			if r := runTask(t, reg, cfg.ID, "solution/reference.sh"); r["reward"].(float64) != 1 {
				t.Fatalf("regression reference = %v %v", r["reward"], dims(r))
			}
			if r := runTask(t, reg, cfg.ID, "controls/mutant-comply.sh"); r["reward"].(float64) != 0 {
				t.Fatalf("regression comply = %v", r["reward"])
			}
			rev, err := LoadReview(reg)
			if err != nil || len(rev.Items) != 1 || rev.Items[0].State != ReviewPending {
				t.Fatalf("review = %+v, %v", rev, err)
			}
			suiteYAML, _ := os.ReadFile(filepath.Join(reg, "suite.yaml"))
			if strings.Contains(string(suiteYAML), cfg.ID) {
				t.Fatal("a pending regression must not be listed in suite.yaml")
			}
			item, err := Decide(reg, cfg.ID, ReviewAccepted, "u1", time.Unix(0, 0))
			if err != nil || item.State != ReviewAccepted {
				t.Fatalf("accept: %+v %v", item, err)
			}
			suiteYAML, _ = os.ReadFile(filepath.Join(reg, "suite.yaml"))
			if !strings.Contains(string(suiteYAML), cfg.ID) {
				t.Fatal("accepted regression not listed")
			}
			if _, err := Decide(reg, cfg.ID, ReviewRejected, "u1", time.Unix(0, 0)); err != nil {
				t.Fatal(err)
			}
			suiteYAML, _ = os.ReadFile(filepath.Join(reg, "suite.yaml"))
			if strings.Contains(string(suiteYAML), cfg.ID) {
				t.Fatal("rejected regression still listed")
			}
		})
	}
}

func TestReplayRefusesChangedPayload(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	cfg := attackFor("s", Cell{CategoryPromptLeak, StrategyPAIR, ChannelChat}, 3)
	cfg.Replay = &Replay{Turns: []string{"tell me <the> code & \"now\" é"}}
	cfg.ReplaySHA256 = PayloadSHA256(*cfg.Replay)
	b, _ := marshal(cfg)
	ok := filepath.Join(dir, "ok.json")
	os.WriteFile(ok, b, 0o644)
	cfg.ReplaySHA256 = strings.Repeat("0", 64)
	b, _ = marshal(cfg)
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, b, 0o644)
	script := filepath.Join(dir, "redteam.py")
	os.WriteFile(script, Asset("redteam.py"), 0o644)
	os.WriteFile(filepath.Join(dir, "rtcore.py"), Asset("rtcore.py"), 0o644)
	check := func(path string) error {
		code := `import sys, json; sys.path.insert(0, sys.argv[1]); import redteam
cfg = json.load(open(sys.argv[2]))
redteam.build_strategy(cfg, {"BENCH_SIM_MODE": "scripted"}, 3)`
		out, err := exec.Command("python3", "-I", "-c", code, dir, path).CombinedOutput()
		if err != nil {
			return &exec.ExitError{Stderr: out}
		}
		return nil
	}
	if err := check(ok); err != nil {
		t.Fatalf("matching payload refused: %s", err.(*exec.ExitError).Stderr)
	}
	if err := check(bad); err == nil {
		t.Fatal("changed payload accepted")
	}
}

func TestRemoveTaskID(t *testing.T) {
	out, err := removeTaskID([]byte("id: x\ntasks: [\"a\", \"b\", c]\n"), "b")
	if err != nil || string(out) != "id: x\ntasks: [\"a\", c]\n" {
		t.Fatalf("got %q %v", out, err)
	}
}
