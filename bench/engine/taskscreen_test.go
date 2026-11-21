package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// warnLog records Warn lines so a test can check the planner said why it
// skipped a task.
type warnLog struct {
	schemas.NoopLogger
	mu    sync.Mutex
	lines []string
}

func (l *warnLog) Warn(msg string, kv ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(append([]any{msg}, kv...)...))
}

func (l *warnLog) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// screenBench is the planner test bench with a suite root on disk, t07 and
// t08 held out, and t02 quarantined in the sidecar.
func screenBench(t *testing.T) *bench {
	t.Helper()
	b := newBench(t)
	b.suite.Root = filepath.Join(b.root, "benchmarks", "coding-core")
	b.suite.TaskByID("t07").Split = schemas.SplitHeldOut
	b.suite.TaskByID("t08").Split = schemas.SplitHeldOut
	f := schemas.SuiteHealthFile{Suite: "coding-core", Quarantined: map[string]schemas.TaskQuarantine{
		"t02": {Reason: "verifier flaky on reference", At: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), Source: schemas.QuarantineManual},
	}}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b.suite.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.suite.Root, schemas.SuiteHealthFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPlanScreensSplitAndQuarantine(t *testing.T) {
	all := schemas.TaskSelector{All: true}
	tests := []struct {
		name      string
		cond      schemas.ConditionSpec
		include   bool
		wantTasks []string
		wantHeld  []string
		wantErr   string
		wantWarn  string
	}{
		{name: "all is public minus quarantined", cond: schemas.ConditionSpec{ID: "clean", Tasks: all, Trials: 1},
			wantTasks: []string{"t01", "t03", "t04", "t05", "t06"}, wantWarn: "skipping quarantined task"},
		{name: "explicit public split all", cond: schemas.ConditionSpec{ID: "clean", Tasks: all, Trials: 1, Split: "public"},
			wantTasks: []string{"t01", "t03", "t04", "t05", "t06"}},
		{name: "held out split", cond: schemas.ConditionSpec{ID: "clean", Tasks: all, Trials: 1, Split: "held_out"},
			wantTasks: []string{"t07", "t08"}, wantHeld: []string{"t07", "t08"}},
		{name: "explicit ids may name held out", cond: schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t07"}}, Trials: 1},
			wantTasks: []string{"t01", "t07"}, wantHeld: []string{"t07"}},
		{name: "explicit quarantined refused", cond: schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 1},
			wantErr: "names task t02, which is quarantined (verifier flaky on reference); pass -include-quarantined"},
		{name: "explicit quarantined included", cond: schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 1},
			include: true, wantTasks: []string{"t01", "t02"}},
		{name: "bad split", cond: schemas.ConditionSpec{ID: "clean", Tasks: all, Trials: 1, Split: "secret"},
			wantErr: "split must be public or held_out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := screenBench(t)
			log := &warnLog{}
			m, err := NewPlanner(b.reg, b.root, log, WithIncludeQuarantined(tt.include)).Plan(context.Background(), testExperiment(tt.cond))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			var tasks []string
			for _, s := range m.Slots {
				if !slices.Contains(tasks, s.Task) {
					tasks = append(tasks, s.Task)
				}
			}
			slices.Sort(tasks)
			if !slices.Equal(tasks, tt.wantTasks) {
				t.Fatalf("planned tasks %v, want %v", tasks, tt.wantTasks)
			}
			if !slices.Equal(m.HeldOutTasks, tt.wantHeld) {
				t.Fatalf("held_out_tasks %v, want %v", m.HeldOutTasks, tt.wantHeld)
			}
			if tt.wantWarn != "" && !strings.Contains(log.joined(), tt.wantWarn) {
				t.Fatalf("warnings %q, want %q", log.joined(), tt.wantWarn)
			}
		})
	}
}

// TestPlanPublicManifestKeepsEncoding checks a public-only plan carries no
// held_out_tasks key, so manifests planned before the split existed hash the
// same.
func TestPlanPublicManifestKeepsEncoding(t *testing.T) {
	b := newBench(t)
	m := b.plan(t, testExperiment())
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"held_out_tasks"`, `"split"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("public manifest carries %s", key)
		}
	}
}

func TestPlanAllQuarantinedRefused(t *testing.T) {
	b := screenBench(t)
	cond := schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1, Split: schemas.SplitHeldOut}
	for _, id := range []string{"t07", "t08"} {
		b.suite.TaskByID(id).Split = ""
	}
	_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), testExperiment(cond))
	if err == nil || !strings.Contains(err.Error(), "selects no tasks after the held_out split and the quarantine") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadQuarantine(t *testing.T) {
	dir := t.TempDir()
	q, err := readQuarantine(dir)
	if err != nil || q != nil {
		t.Fatalf("missing sidecar: %v %v", q, err)
	}
	if err := os.WriteFile(filepath.Join(dir, schemas.SuiteHealthFileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readQuarantine(dir); err == nil {
		t.Fatal("a broken sidecar must fail the plan, not silently run quarantined tasks")
	}
}

// TestReplanIgnoresQuarantine checks a worker's replan reproduces a manifest
// frozen before a task was quarantined.
func TestReplanIgnoresQuarantine(t *testing.T) {
	b := screenBench(t)
	if err := os.Remove(filepath.Join(b.suite.Root, schemas.SuiteHealthFileName)); err != nil {
		t.Fatal(err)
	}
	cond := schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1}
	frozen := b.plan(t, testExperiment(cond))
	data, err := json.Marshal(schemas.SuiteHealthFile{Quarantined: map[string]schemas.TaskQuarantine{"t03": {Reason: "later"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.suite.Root, schemas.SuiteHealthFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	e := frozen.Experiment
	replan, err := NewPlanner(b.reg, b.root, nil, WithIgnoreQuarantine()).Plan(context.Background(), &e)
	if err != nil {
		t.Fatal(err)
	}
	if len(replan.Slots) != len(frozen.Slots) {
		t.Fatalf("replan has %d slots, frozen %d", len(replan.Slots), len(frozen.Slots))
	}
	fresh, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), &e)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Slots) >= len(frozen.Slots) {
		t.Fatal("a new plan did not skip the task quarantined after the freeze")
	}
}

// TestFrozenReplanKeepsPreFreezeQuarantine is 2-C-11: a task already
// quarantined when the coordinator froze `tasks: all` must stay out of a
// worker's replan of that manifest, while a task quarantined after the freeze
// stays in, so an identical checkout reproduces the frozen hash.
func TestFrozenReplanKeepsPreFreezeQuarantine(t *testing.T) {
	b := screenBench(t)
	write := func(ids ...string) {
		q := map[string]schemas.TaskQuarantine{}
		for _, id := range ids {
			q[id] = schemas.TaskQuarantine{Reason: "flaky"}
		}
		data, err := json.Marshal(schemas.SuiteHealthFile{Quarantined: q})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(b.suite.Root, schemas.SuiteHealthFileName), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("t02")
	cond := schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1}
	frozen := b.plan(t, testExperiment(cond))
	write("t02", "t03") // t03 quarantined after the freeze
	e := frozen.Experiment
	worker := NewPlanner(b.reg, b.root, nil, WithIgnoreQuarantine())
	replan, err := worker.Plan(WithFrozenTasks(context.Background(), frozen), &e)
	if err != nil {
		t.Fatal(err)
	}
	adoptUnrepeatable(replan, frozen)
	h, err := ManifestHash(replan)
	if err != nil {
		t.Fatal(err)
	}
	if h != frozen.Hash {
		t.Fatalf("frozen replan hash %s, frozen %s; differs in %s", h, frozen.Hash, manifestDiff(replan, frozen))
	}
}

// TestFrozenReplanCleanCheckoutKeepsQuarantine is QC2-11-05: a worker with
// the same suite lock but no quarantine sidecar must not add back a task the
// coordinator's quarantine dropped before the freeze.
func TestFrozenReplanCleanCheckoutKeepsQuarantine(t *testing.T) {
	b := screenBench(t)
	side := filepath.Join(b.suite.Root, schemas.SuiteHealthFileName)
	data, err := json.Marshal(schemas.SuiteHealthFile{Quarantined: map[string]schemas.TaskQuarantine{"t02": {Reason: "flaky"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(side, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cond := schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1}
	frozen := b.plan(t, testExperiment(cond))
	if err := os.Remove(side); err != nil { // the worker's clean checkout
		t.Fatal(err)
	}
	e := frozen.Experiment
	replan, err := NewPlanner(b.reg, b.root, nil, WithIgnoreQuarantine()).Plan(WithFrozenTasks(context.Background(), frozen), &e)
	if err != nil {
		t.Fatal(err)
	}
	adoptUnrepeatable(replan, frozen)
	if h, err := ManifestHash(replan); err != nil || h != frozen.Hash {
		t.Fatalf("clean-checkout replan hash %s, frozen %s (%v); differs in %s", h, frozen.Hash, err, manifestDiff(replan, frozen))
	}
}
