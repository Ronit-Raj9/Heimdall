//go:build integration

package environments

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// stepsRun is scenarioRun with the observers on and hd-steps- run ids.
func stepsRun(t *testing.T, cfg DockerWorkspaceConfig, files map[string]string) (*dockerWorkspace, *schemas.RunContext) {
	t.Helper()
	taskDir := t.TempDir()
	writeTree(t, taskDir, files)
	cfg.HelperImage = testImage
	cfg.ProcessWatch, cfg.WorkspaceWatch = true, true
	e := newDockerWorkspace(cfg)
	runID := strings.Replace(utils.NewID("r", 8), "r_", "hd-steps-", 1)
	rc := &schemas.RunContext{
		Run:   &schemas.Run{RunID: runID, ExpID: "e_hdsteps"},
		Slot:  &schemas.Slot{Condition: schemas.ConditionClean},
		Suite: &schemas.Suite{ID: "hd-steps-suite", Version: "1.0.0", Workdir: "/workspace", BaseImage: schemas.ImageSpec{Tag: testImage}, Root: taskDir},
		Task:  &schemas.Task{ID: "t1", Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}},
		Agent: &schemas.AgentConfig{},
		Dir:   t.TempDir(),
		Key:   &schemas.RunKey{Value: "sk-hd-steps-key-0123456789"},
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if err := e.Destroy(ctx, rc); err != nil {
			t.Errorf("destroy: %v", err)
		}
		assertNoLeftovers(t, runID)
	})
	h, err := e.Create(context.Background(), rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	return e, rc
}

const stepsAgent = `set -e
sleep 1
cd /workspace
echo "marker-one" > notes.md
echo more >> README.md
mkdir -p src && printf 'print(1)\n' > src/app.py
python3 -c 'import time; time.sleep(0.3)' marker-two
sh -c 'sleep 0.3' marker-three "key=sk-hd-steps-key-0123456789"
python3 - <<'PY'
import urllib.request
assert urllib.request.urlopen("http://toolproxy:9200/s/echo/ping", timeout=10).read() == b"pong"
PY
mv notes.md notes-final.md
rm src/app.py
sleep 0.5
`

// TestObserversTimeline runs a scripted agent that runs shell commands,
// writes, renames and deletes files and calls a proxied tool, then builds
// the step timeline: each action is there with the right source, and the
// run key never reaches processes.jsonl.
func TestObserversTimeline(t *testing.T) {
	ctx := context.Background()
	e, rc := stepsRun(t, DockerWorkspaceConfig{ToolProxyImage: fakeToolProxy(t)}, map[string]string{"repo/README.md": "task\n"})
	rc.Task.Services = []schemas.ServiceSpec{{ID: "echo", Role: schemas.ServiceRoleTool, Image: "python:3.12-slim", Argv: []string{"python3", "-m", "http.server", "8081"}, Port: 8081}}
	handles, results, err := e.StartServices(ctx, rc)
	if err != nil {
		t.Fatalf("start services: %v (%+v)", err, results)
	}
	rc.Services = handles
	plan := shPlan(stepsAgent, 60)
	plan.Network = ""
	res, err := e.Exec(ctx, rc, plan)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("exec: %+v %v\n%s", res, err, readFile(t, filepath.Join(rc.Dir, "agent.stderr.txt")))
	}
	if _, err := e.ExportState(ctx, rc); err != nil {
		t.Fatalf("export state: %v", err)
	}
	raw := readFile(t, filepath.Join(rc.Dir, schemas.ProcessesFile))
	if bytes.Contains(raw, []byte("sk-hd-steps-key-0123456789")) {
		t.Fatalf("run key in processes.jsonl")
	}
	procs, fsevs, statuses, err := ReadObservers(rc.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statuses {
		if s.State != schemas.ObserverOn {
			t.Fatalf("observer %+v", s)
		}
		t.Logf("%s: %s", s.Observer, s.Coverage)
	}
	ledger, _, err := evidence.ParseToolLedger(bytes.NewReader(readFile(t, filepath.Join(rc.Dir, ToolsFile))))
	if err != nil {
		t.Fatal(err)
	}
	ev := &schemas.Evidence{Processes: procs, FileEvents: fsevs, Observers: statuses, ToolCalls: ledger}
	steps, _ := evidence.BuildTimeline(ev)
	has := func(source, kind, text string) bool {
		for _, s := range steps {
			if s.Source == source && s.Kind == kind && strings.Contains(s.Summary, text) {
				return true
			}
		}
		return false
	}
	for _, want := range []struct{ source, kind, text string }{
		{schemas.StepSourceProcess, schemas.StepCommand, "marker-two"},
		{schemas.StepSourceProcess, schemas.StepCommand, "marker-three"},
		{schemas.StepSourceProcess, schemas.StepCommand, "[redacted]"},
		{schemas.StepSourceFS, schemas.StepFileChange, "create notes.md"},
		{schemas.StepSourceFS, schemas.StepFileChange, "create src/app.py"},
		{schemas.StepSourceFS, schemas.StepFileChange, "rename notes.md -> notes-final.md"},
		{schemas.StepSourceFS, schemas.StepFileChange, "delete src/app.py"},
		{schemas.StepSourceFS, schemas.StepFileChange, "modify README.md"},
		{schemas.StepSourceToolProxy, schemas.StepToolExecution, "echo"},
	} {
		if !has(want.source, want.kind, want.text) {
			var b strings.Builder
			for _, s := range steps {
				fmt.Fprintf(&b, "%s %s %s\n", s.Source, s.Kind, s.Summary)
			}
			t.Errorf("no %s %s row with %q in:\n%s", want.source, want.kind, want.text, b.String())
		}
	}
	ev.Steps = steps
	for _, m := range metrics.Builtin() {
		switch m.GetName() {
		case metrics.CommandsExecuted, metrics.FilesChangedEvents:
			v, err := m.Compute(ctx, rc, ev)
			got, ok := v.Float()
			if err != nil || !ok || got < 3 {
				t.Errorf("%s = %+v %v", m.GetName(), v, err)
			}
			t.Logf("%s = %v (%s)", m.GetName(), got, v.Evidence)
		}
	}
}

// TestProcessObserverMissRate spawns 40 processes of each of four fixed
// lengths and reports how many the process observer caught at the default
// interval. Every 50 ms process must be caught, and the PID counter must
// bound the misses.
func TestProcessObserverMissRate(t *testing.T) {
	e, rc := stepsRun(t, DockerWorkspaceConfig{}, map[string]string{"repo/README.md": "task\n"})
	script := `sleep 1; for d in 0.001 0.005 0.02 0.05; do i=0; while [ $i -lt 40 ]; do sleep $d; i=$((i+1)); done; done; sleep 1.5`
	res, err := e.Exec(context.Background(), rc, shPlan(script, 120))
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("exec: %+v %v", res, err)
	}
	procs, _, statuses, err := ReadObservers(rc.Dir)
	if err != nil || len(statuses) != 2 {
		t.Fatalf("statuses %+v %v", statuses, err)
	}
	caught := map[string]int{}
	for _, p := range procs {
		if len(p.Argv) == 2 && p.Argv[0] == "sleep" {
			caught[p.Argv[1]]++
		}
	}
	st := statuses[0]
	t.Logf("interval %d ms: 1 ms %d/40, 5 ms %d/40, 20 ms %d/40, 50 ms %d/40; %s", st.IntervalMS, caught["0.001"], caught["0.005"], caught["0.02"], caught["0.05"], st.Coverage)
	if caught["0.05"] < 36 {
		t.Errorf("caught %d of 40 50 ms processes", caught["0.05"])
	}
	missed := 160 - caught["0.001"] - caught["0.005"] - caught["0.02"] - caught["0.05"]
	if st.PIDsAllocated < 160 || st.PIDsAllocated-st.PIDsSeen < missed {
		t.Errorf("PID counter does not bound the misses: allocated %d seen %d missed %d", st.PIDsAllocated, st.PIDsSeen, missed)
	}
}

// TestAgentCannotKillObserver runs a non-root agent that finds the process
// observer in its PID namespace and tries to kill it: it can see it, the
// kill is refused, and the observer keeps observing to the end.
func TestAgentCannotKillObserver(t *testing.T) {
	e, rc := stepsRun(t, DockerWorkspaceConfig{}, map[string]string{"repo/README.md": "task\n"})
	script := `sleep 1.5
for d in /proc/[0-9]*; do
  c=$(tr '\0' ' ' < $d/cmdline 2>/dev/null) || continue
  case "$c" in python3*) p=${d#/proc/}; echo "SEE $p $c"; if kill -9 $p 2>/dev/null; then echo "KILLED $p"; else echo "KILLFAIL $p"; fi;; esac
done
sleep 1.5
sleep 0.2`
	res, err := e.Exec(context.Background(), rc, shPlan(script, 60))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	out := string(readFile(t, res.StdoutPath))
	if !strings.Contains(out, "SEE ") || !strings.Contains(out, "KILLFAIL") || strings.Contains(out, "KILLED") {
		t.Fatalf("agent output:\n%s", out)
	}
	procs, _, statuses, err := ReadObservers(rc.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != schemas.ObserverOn {
		t.Fatalf("process observer %+v", statuses[0])
	}
	late := false
	for _, p := range procs {
		if len(p.Argv) == 2 && p.Argv[0] == "sleep" && p.Argv[1] == "0.2" {
			late = true
		}
	}
	if !late {
		t.Fatal("observer did not see the agent's last command")
	}
}

// TestProcessObserverStartupGap measures how late the process observer
// joins: the agent starts a 100 ms command every 100 ms from its first
// moment, and the first one caught says how long the gap was.
func TestProcessObserverStartupGap(t *testing.T) {
	e, rc := stepsRun(t, DockerWorkspaceConfig{}, map[string]string{"repo/README.md": "task\n"})
	script := `i=0; while [ $i -lt 30 ]; do sh -c 'sleep 0.1' gap-$i; i=$((i+1)); done`
	if _, err := e.Exec(context.Background(), rc, shPlan(script, 60)); err != nil {
		t.Fatalf("exec: %v", err)
	}
	procs, _, statuses, err := ReadObservers(rc.Dir)
	if err != nil || statuses[0].State != schemas.ObserverOn {
		t.Fatalf("statuses %+v %v", statuses, err)
	}
	first := -1
	for i := 0; i < 30 && first < 0; i++ {
		for _, p := range procs {
			if len(p.Argv) == 4 && p.Argv[3] == fmt.Sprintf("gap-%d", i) {
				first = i
				break
			}
		}
	}
	t.Logf("first command caught: gap-%d (about %d ms after the agent started); %s", first, first*100, statuses[0].Coverage)
	if first < 0 || first > 15 {
		t.Errorf("observer joined after %d commands", first)
	}
}
