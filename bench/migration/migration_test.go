package migration

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var update = flag.Bool("update", false, "rewrite the golden report")

// fixture: one agent on three models over ten tasks, one trial, clean and
// model-outage. A passes t01-t03 clean; B passes every task (7 better); C
// passes none (3 worse). Under model-outage every arm fails every task but
// B, which passes t01.
func fixture() Input {
	m := &schemas.Manifest{ExpID: "e_mig", SuiteID: "s", SuiteVersion: "1", Arms: map[string]schemas.ArmManifest{}}
	m.Experiment.Primary.Condition = schemas.ConditionClean
	for arm, model := range map[string]string{"A": "old-model", "B": "new-model", "C": "nano-model"} {
		m.Arms[arm] = schemas.ArmManifest{Label: model, ConfigHash: "hash" + arm, Config: schemas.AgentConfig{ID: "agent", Model: model}}
	}
	tasks := map[string]*schemas.Task{}
	var runs []schemas.SealedRun
	n := 0
	add := func(arm, task, cond string, pass bool, gate bool) {
		n++
		outcome := schemas.OutcomeFail
		if pass {
			outcome = schemas.OutcomePass
		}
		cost := map[string]float64{"A": 0.10, "B": 0.30, "C": 0.02}[arm]
		sr := schemas.SealedRun{
			Slot: schemas.Slot{SlotID: fmt.Sprintf("s%d", n), PairID: "p_" + task + "_" + cond, Task: task, Arm: arm, Condition: cond, Trial: 1, Counted: true},
			Run:  schemas.Run{RunID: fmt.Sprintf("r_%s_%s_%s", arm, task, cond), Outcome: outcome, Timing: schemas.RunTiming{Started: time.Date(2026, 11, 1, 0, 0, n, 0, time.UTC)}},
			Metrics: map[string]schemas.Value{
				"cost_usd":         schemas.Observed(cost, schemas.EvidenceGateway),
				"false_completion": schemas.NotApplicable(),
			},
		}
		if gate {
			sr.Gates = []schemas.GateResult{{Gate: "g1_protected_paths", Effect: "veto", Fired: true}}
		}
		runs = append(runs, sr)
	}
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("t%02d", i)
		kind, diff := schemas.TaskBugfix, "easy"
		if i > 5 {
			kind, diff = schemas.TaskFeature, "hard"
		}
		tasks[id] = &schemas.Task{ID: id, Kind: kind, Difficulty: diff, Capabilities: []string{"tool_use"}}
		add("A", id, schemas.ConditionClean, i <= 3, false)
		add("B", id, schemas.ConditionClean, true, i == 10)
		add("C", id, schemas.ConditionClean, false, false)
		if i <= 4 {
			add("A", id, "model-outage", false, false)
			add("B", id, "model-outage", i == 1, false)
			add("C", id, "model-outage", false, false)
		}
	}
	return Input{Manifest: m, Runs: runs, Tasks: tasks}
}

func build(t *testing.T, in Input, q Query) *Report {
	t.Helper()
	r, err := Build(context.Background(), in, q)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return r
}

func pairTo(t *testing.T, r *Report, arm string) Pair {
	t.Helper()
	for _, p := range r.Pairs {
		if p.To.Arm == arm {
			return p
		}
	}
	t.Fatalf("no pair to %s", arm)
	return Pair{}
}

func stratum(t *testing.T, p Pair, dim, value string) Verdict {
	t.Helper()
	for _, s := range p.Strata {
		if s.Dimension == dim && s.Value == value {
			return s
		}
	}
	t.Fatalf("no stratum %s %s", dim, value)
	return Verdict{}
}

func TestOverallVerdictsAreHolmAdjustedAcrossPairs(t *testing.T) {
	r := build(t, fixture(), Query{From: "old-model"})
	if len(r.Pairs) != 2 {
		t.Fatalf("pairs = %d, want A to B and A to C", len(r.Pairs))
	}
	b, c := pairTo(t, r, "B").Overall, pairTo(t, r, "C").Overall
	// B: 7 better of 10, exact p = 2/2^7; Holm across 2 pairs doubles the
	// smaller p.
	if *b.P != 2.0/128 || *b.PAdjusted != 4.0/128 || b.Result != ResultBetter || b.Family != 2 {
		t.Errorf("A to B overall = %+v, want p 2/128, adjusted 4/128, better, family 2", b)
	}
	// C: 3 worse, p = 2/8 = 0.25: cannot tell, and an estimate of the tasks
	// needed.
	if *c.P != 0.25 || c.Result != ResultCannotTell || c.Worse != 3 || c.TasksNeeded == 0 {
		t.Errorf("A to C overall = %+v, want p 0.25, cannot tell, 3 worse, tasks needed", c)
	}
}

func TestStrataStatistics(t *testing.T) {
	r := build(t, fixture(), Query{From: "A", To: []string{"B"}})
	p := pairTo(t, r, "B")
	feature := stratum(t, p, DimTaskKind, string(schemas.TaskFeature))
	if feature.Tasks != 5 || feature.Better != 5 || *feature.P != 2.0/32 {
		t.Errorf("feature stratum = %+v, want 5 tasks, 5 better, p 1/16", feature)
	}
	bugfix := stratum(t, p, DimTaskKind, string(schemas.TaskBugfix))
	if bugfix.Tasks != 5 || bugfix.Better != 2 || *bugfix.P != 0.5 {
		t.Errorf("bugfix stratum = %+v, want 5 tasks, 2 better, p 0.5", bugfix)
	}
	outage := stratum(t, p, DimCondition, "model-outage")
	if outage.Tasks != 4 || outage.Better != 1 || outage.Result != ResultCannotTell {
		t.Errorf("model-outage stratum = %+v, want 4 tasks, 1 better, cannot tell", outage)
	}
	// Every stratum is one Holm family: the family size is the number of
	// strata with a test, and no adjusted p is below its raw p.
	for _, s := range p.Strata {
		if s.Family != len(p.Strata) {
			t.Errorf("%s %s family = %d, want %d", s.Dimension, s.Value, s.Family, len(p.Strata))
		}
		if *s.PAdjusted < *s.P {
			t.Errorf("%s %s adjusted %g below raw %g", s.Dimension, s.Value, *s.PAdjusted, *s.P)
		}
	}
	// The clean stratum has the overall p (2/128) but is one of 8 strata:
	// Holm multiplies the smallest p by 8, so it no longer separates. The
	// strata never claim more than their family allows.
	clean := stratum(t, p, DimCondition, schemas.ConditionClean)
	if clean.Family != 8 || *clean.PAdjusted != 8*2.0/128 || clean.Result != ResultCannotTell {
		t.Errorf("clean stratum = %+v, want family 8, adjusted p 0.125, cannot tell", clean)
	}
	for _, dim := range []string{DimCapability, DimDifficultyDeclared, DimDifficultyObserved} {
		found := false
		for _, s := range p.Strata {
			found = found || s.Dimension == dim
		}
		if !found {
			t.Errorf("no %s stratum", dim)
		}
	}
}

func TestRecommendationNeverOutrunsTheStatistics(t *testing.T) {
	r := build(t, fixture(), Query{From: "old-model"})
	if a := pairTo(t, r, "B").Recommendation.Action; a != ActionMigrate {
		t.Errorf("A to B = %s, want migrate (significantly better, no stratum worse)", a)
	}
	if a := pairTo(t, r, "C").Recommendation.Action; a != ActionNotEnough {
		t.Errorf("A to C = %s, want not enough evidence (3 worse tasks cannot be told apart)", a)
	}
	worse := Pair{Overall: Verdict{Result: ResultCannotTell, Reason: "cannot tell"}, Strata: []Verdict{{Dimension: DimCondition, Value: "model-outage", Result: ResultWorse, Reason: "new model worse"}}}
	if rec := recommend(worse, nil, false, nil); rec.Action != ActionDoNotMigrate || !strings.Contains(rec.Reason, "model-outage") {
		t.Errorf("a significantly worse stratum = %+v, want do not migrate naming it", rec)
	}
	better := Pair{Overall: Verdict{Result: ResultBetter, Condition: "clean", Reason: "new model better"}}
	block := &schemas.PolicyResult{PolicyName: "p", Decision: schemas.PolicyBlock, Checks: []schemas.PolicyCheckResult{{ID: "no-integrity-violations", Status: schemas.PolicyFail, Blocking: true, Reason: "1 veto"}}}
	if rec := recommend(better, block, false, nil); rec.Action != ActionDoNotMigrate {
		t.Errorf("better but the policy blocks on a failed check = %s, want do not migrate", rec.Action)
	}
	unknownBlock := &schemas.PolicyResult{PolicyName: "p", Decision: schemas.PolicyBlock, Checks: []schemas.PolicyCheckResult{{ID: "fc", Status: schemas.PolicyUnknown, Blocking: true}}}
	if rec := recommend(better, unknownBlock, false, nil); rec.Action != ActionNotEnough {
		t.Errorf("better but the policy blocks on an unknown = %s, want not enough evidence", rec.Action)
	}
	if rec := recommend(better, nil, true, nil); rec.Action != ActionNotEnough {
		t.Errorf("synthetic = %s, want not enough evidence", rec.Action)
	}
}

func TestBehaviourAndFlippedTasks(t *testing.T) {
	r := build(t, fixture(), Query{From: "A", To: []string{"B"}})
	p := pairTo(t, r, "B")
	if len(p.Behaviour.Gates) != 1 || p.Behaviour.Gates[0].Status != "new" || p.Behaviour.Gates[0].To != 1 {
		t.Errorf("gates = %+v, want g1 new in B", p.Behaviour.Gates)
	}
	regressed, fixed := 0, 0
	for _, f := range p.Flipped {
		switch f.Kind {
		case "regressed":
			regressed++
		case "fixed":
			fixed++
			if len(f.Runs) != 2 || f.Runs[0].PairID == "" {
				t.Errorf("flipped %s runs = %+v, want both runs with pair ids", f.Task, f.Runs)
			}
		}
	}
	if regressed != 0 || fixed != 8 {
		t.Errorf("flipped: %d regressed, %d fixed; want 0 and 8 (7 clean, 1 outage)", regressed, fixed)
	}
	if len(p.Behaviour.Conditions) != 2 {
		t.Errorf("conditions = %+v, want clean and model-outage", p.Behaviour.Conditions)
	}
	if !strings.Contains(strings.Join(r.Notes, " "), "not a tuned prompt") {
		t.Errorf("notes = %v, want the tuned-prompt note", r.Notes)
	}
}

func TestQueryErrors(t *testing.T) {
	in := fixture()
	for _, q := range []Query{{From: ""}, {From: "no-such-model"}, {From: "A", To: []string{"zzz"}}, {From: "A", Alpha: 2}} {
		if _, err := Build(context.Background(), in, q); err == nil {
			t.Errorf("query %+v: want an error", q)
		}
	}
}

func TestReportGolden(t *testing.T) {
	got := Markdown(build(t, fixture(), Query{From: "old-model"}))
	path := filepath.Join("testdata", "report.golden.md")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to write it): %v", err)
	}
	if got != string(want) {
		t.Errorf("report differs from %s (run go test ./migration -update and review the diff):\n%s", path, got)
	}
}
