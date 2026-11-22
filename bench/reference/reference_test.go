package reference

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

var t0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

func doneEval(id string, children ...schemas.EvaluationChild) *schemas.Evaluation {
	return &schemas.Evaluation{
		ID: id, Status: schemas.EvaluationDone,
		Spec:     schemas.EvaluationSpec{Mode: schemas.EvaluationModeScore, Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Version: 2, Model: "gpt-5.4"}}},
		Children: children,
	}
}

func report(ev *schemas.Evaluation, rows ...reportcard.Benchmark) *reportcard.Report {
	return &reportcard.Report{Evaluation: ev.ID, Arms: []reportcard.Arm{{Label: "A", Agent: "codex", Version: 2, Model: "gpt-5.4"}}, Benchmarks: rows}
}

func TestEntriesFromVerifiedReport(t *testing.T) {
	ev := doneEval("ev_one", schemas.EvaluationChild{Benchmark: "hd-swe-mini", ExperimentID: "e_a"}, schemas.EvaluationChild{Benchmark: "tau2", ExperimentID: "e_b"})
	rep := report(ev,
		reportcard.Benchmark{Benchmark: "hd-swe-mini", Capability: "coding", ExperimentID: "e_a", Arms: map[string]reportcard.Metrics{
			"A": {Score: 0.5, CILow: 0.3, CIHigh: 0.7, Runs: 16, Tasks: 8, CostUSD: f(4), WallS: 120},
		}},
		reportcard.Benchmark{Benchmark: "tau2", Capability: "support", ExperimentID: "e_b", Arms: map[string]reportcard.Metrics{
			"A": {Score: 0, Runs: 4, Tasks: 2, CostUSD: f(1)},
		}},
	)
	man := &schemas.Manifest{ExpID: "e_a", SuiteVersion: "1.2.0", SuiteLockHash: "sha256:lock", Arms: map[string]schemas.ArmManifest{
		"A": {Label: "A", Config: schemas.AgentConfig{ID: "codex", AgentVersion: "0.50.0"}},
	}}
	es, skipped, err := Entries(Input{
		Evaluation: ev, Report: rep,
		Seals:     map[string]Seal{"e_a": {Head: "sha256:head_a", Runs: 16, RanAt: t0}, "e_b": {Head: "sha256:head_b", Runs: 4, RanAt: t0}},
		Manifests: map[string]*schemas.Manifest{"e_a": man},
		Library:   func(string) bool { return true }, PublishedBy: "ops", Now: t0,
	})
	if err != nil || len(skipped) != 0 || len(es) != 2 {
		t.Fatalf("entries %+v skipped %v err %v", es, skipped, err)
	}
	a := es[0]
	if a.ID != "ev_one:hd-swe-mini:A" || a.Solved != 8 || a.CostPerSolvedUSD == nil || *a.CostPerSolvedUSD != 0.5 || a.SealHash != "sha256:head_a" || a.SealedRuns != 16 {
		t.Fatalf("entry %+v", a)
	}
	if a.AgentBuild != "0.50.0" || a.LockHash != "sha256:lock" || a.BenchmarkVersion != "1.2.0" || a.Version != 2 || a.PublishedBy != "ops" {
		t.Fatalf("identity %+v", a)
	}
	// Nothing solved: the cost is known, the cost per solved task is not.
	if b := es[1]; b.Solved != 0 || b.CostUSD == nil || b.CostPerSolvedUSD != nil {
		t.Fatalf("unsolved entry %+v", b)
	}
}

func TestEntriesRefuse(t *testing.T) {
	ev := doneEval("ev_one", schemas.EvaluationChild{Benchmark: "own-suite", ExperimentID: "e_a"})
	rep := report(ev, reportcard.Benchmark{Benchmark: "own-suite", ExperimentID: "e_a", Arms: map[string]reportcard.Metrics{"A": {Score: 1, Runs: 2}}})
	seals := map[string]Seal{"e_a": {Head: "h", Runs: 2}}
	lib := func(id string) bool { return id != "own-suite" }
	if _, _, err := Entries(Input{Evaluation: ev, Report: rep, Seals: seals, Library: lib}); err == nil || !strings.Contains(err.Error(), "library") {
		t.Fatalf("own benchmark published: %v", err)
	}
	all := func(string) bool { return true }
	if _, _, err := Entries(Input{Evaluation: ev, Report: rep, Seals: map[string]Seal{}, Library: all}); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("unverified experiment published: %v", err)
	}
	if _, _, err := Entries(Input{Evaluation: ev, Report: rep, Seals: seals}); err == nil {
		t.Fatal("published without a library")
	}
	running := *ev
	running.Status = schemas.EvaluationRunning
	if _, _, err := Entries(Input{Evaluation: &running, Report: rep, Seals: seals, Library: all}); err == nil {
		t.Fatal("running evaluation published")
	}
	held := *ev
	held.Spec.Split = schemas.SplitHeldOut
	if err := Check(&held); err == nil {
		t.Fatal("held-out evaluation published")
	}
	rerun := *ev
	rerun.RerunOf = "ev_x"
	if err := Check(&rerun); err == nil {
		t.Fatal("rerun published")
	}
	empty := report(ev, reportcard.Benchmark{Benchmark: "own-suite", ExperimentID: "e_a", Arms: map[string]reportcard.Metrics{"A": {}}})
	if _, skipped, err := Entries(Input{Evaluation: ev, Report: empty, Seals: seals, Library: all}); err == nil || len(skipped) != 1 {
		t.Fatalf("no counted run: skipped %v err %v", skipped, err)
	}
}

func TestStorePublishReplaceUnpublish(t *testing.T) {
	root := t.TempDir()
	s := Open(root)
	b, err := s.Load()
	if err != nil || len(b.Entries) != 0 || b.UpdatedAt != nil {
		t.Fatalf("empty board %+v %v", b, err)
	}
	e := func(ev, bench string, score float64) Entry {
		return Entry{ID: ev + ":" + bench + ":A", EvaluationID: ev, Benchmark: bench, Score: score}
	}
	if _, err := s.Publish("ev_a", []Entry{e("ev_a", "tau2", 0.4), e("ev_a", "hd-swe-mini", 0.2)}, "shr_a", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("ev_b", []Entry{e("ev_b", "tau2", 0.6)}, "", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("ev_b", []Entry{e("ev_a", "tau2", 0.6)}, "", t0); err == nil {
		t.Fatal("entry of another evaluation accepted")
	}
	b, _ = s.Load()
	if len(b.Entries) != 3 || b.Entries[0].Benchmark != "hd-swe-mini" || b.Entries[1].EvaluationID != "ev_b" {
		t.Fatalf("order %+v", b.Entries)
	}
	if got := b.ForBenchmark("TAU2@1.0.0"); len(got) != 2 {
		t.Fatalf("for benchmark %+v", got)
	}
	if got := b.Benchmarks(); len(got) != 2 {
		t.Fatalf("benchmarks %v", got)
	}
	// Publishing again replaces, and hands back the old link to revoke.
	prev, err := s.Publish("ev_a", []Entry{e("ev_a", "tau2", 0.5)}, "shr_a2", t0)
	if err != nil || prev != "shr_a" {
		t.Fatalf("prev %q %v", prev, err)
	}
	b, _ = s.Load()
	if len(b.Entries) != 2 {
		t.Fatalf("replace %+v", b.Entries)
	}
	removed, share, err := s.Unpublish("ev_a", t0)
	if err != nil || len(removed) != 1 || share != "shr_a2" {
		t.Fatalf("unpublish %v %q %v", removed, share, err)
	}
	if removed, share, err := s.Unpublish("ev_a", t0); err != nil || removed != nil || share != "" {
		t.Fatalf("second unpublish %v %q %v", removed, share, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".reference", "board.json")); err != nil {
		t.Fatal(err)
	}
}

func TestChainHead(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "e_x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ChainHead(root, "e_x"); err == nil {
		t.Fatal("no seals.log read as a chain")
	}
	if err := os.WriteFile(filepath.Join(root, "e_x", "seals.log"), []byte("r1 sha256:aa\nr2 sha256:bb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head, n, err := ChainHead(root, "e_x")
	if err != nil || head != "sha256:bb" || n != 2 {
		t.Fatalf("%q %d %v", head, n, err)
	}
}

func TestWrap(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_fleet_1", SuiteID: "hd-swe-mini", CreatedAt: t0, Arms: map[string]schemas.ArmManifest{
		"A": {Label: "codex", Config: schemas.AgentConfig{ID: "codex", Model: "gpt-5.4"}},
	}}
	ev, err := Wrap(m, "org_ref", t0)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "ev_ref_e_fleet_1" || ev.Spec.Mode != schemas.EvaluationModeScore || ev.Status != schemas.EvaluationDone || ev.Children[0].ExperimentID != "e_fleet_1" || ev.Org != "org_ref" || ev.CreatedBy != ImportedBy {
		t.Fatalf("wrapped %+v", ev)
	}
	m.Arms["B"] = schemas.ArmManifest{Label: "claude", Config: schemas.AgentConfig{ID: "claude-code", Model: "gpt-5.4"}}
	if ev, _ := Wrap(m, "org_ref", t0); ev.Spec.Mode != schemas.EvaluationModeOpponent {
		t.Fatalf("two agents: %s", ev.Spec.Mode)
	}
	m.Arms["B"] = schemas.ArmManifest{Label: "codex big", Config: schemas.AgentConfig{ID: "codex", Model: "gpt-5.5"}}
	if ev, _ := Wrap(m, "org_ref", t0); ev.Spec.Mode != schemas.EvaluationModeModels {
		t.Fatalf("two models: %s", ev.Spec.Mode)
	}
	m.Arms["C"] = m.Arms["A"]
	if _, err := Wrap(m, "org_ref", t0); err == nil {
		t.Fatal("three agents wrapped")
	}
	long := strings.Repeat("x", 80)
	if id := WrapID(long); !strings.HasPrefix(id, "ev_ref_") || len(id) > 67 {
		t.Fatalf("long id %q", id)
	}
	m2 := &schemas.Manifest{ExpID: "e_h", SuiteID: "s", HeldOutTasks: []string{"t1"}, Arms: map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{ID: "a"}}}}
	if ev, _ := Wrap(m2, "o", t0); ev.Spec.Split != schemas.SplitHeldOut {
		t.Fatal("held-out experiment wrapped as public")
	}
}
