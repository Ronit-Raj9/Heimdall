package reportcard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/failover"
	"github.com/maximhq/bifrost/bench/schemas"
)

// gateSource is a fakeSource that also serves gates.json per run and the
// newest analysis's invalid scope (leaderboard.AnalysisSource).
type gateSource struct {
	*fakeSource
	gates   map[string][]byte
	scope   map[string]string
	reasons map[string][]string
}

func newGateSource() *gateSource {
	return &gateSource{fakeSource: newSource(), gates: map[string][]byte{}, scope: map[string]string{}, reasons: map[string][]string{}}
}

func (g *gateSource) RunFile(runID, name string) ([]byte, error) {
	if b, ok := g.gates[runID]; ok && name == fileGates {
		return b, nil
	}
	return g.fakeSource.RunFile(runID, name)
}

func (g *gateSource) LatestInvalid(expID string) (string, []string, bool) {
	s, ok := g.scope[expID]
	return s, g.reasons[expID], ok
}

// fire marks the first n sealed runs of arm (under condition cond, any when
// "") with a fired invalidate gate.
func (g *gateSource) fire(exp, arm, cond, gate string, n int) {
	b, _ := json.Marshal([]schemas.GateResult{{Gate: gate, Effect: schemas.GateInvalidate, Fired: true}})
	for _, r := range g.runs[exp] {
		if n == 0 {
			return
		}
		if r.Slot.Arm == arm && (cond == "" || r.Slot.Condition == cond) {
			g.gates[r.Run.RunID] = b
			n--
		}
	}
}

func scoreEval(status string, children ...schemas.EvaluationChild) *schemas.Evaluation {
	return &schemas.Evaluation{ID: "ev_s", Status: status, Spec: schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "m"}}},
		Children: children}
}

// N215: a finished child invalid for the whole experiment counts no run, but
// says how many runs were thrown out and why.
func TestReportCardInvalidChildCountsThrownOut(t *testing.T) {
	src := newGateSource()
	var runs []fixRun
	for i := range 24 {
		runs = append(runs, pass("A", "t"+string(rune('a'+i)), 1, nil))
	}
	src.add("e_bad", "hd-harbor-bfcl", []string{"A"}, runs)
	src.decisions["e_bad"] = schemas.DecisionInvalid
	src.scope["e_bad"] = "experiment"
	src.reasons["e_bad"] = []string{"graded coverage 0.500 (12 of 24 runs) in arm A on clean is below 0.90"}
	rep, err := Build(context.Background(), src, scoreEval("done", schemas.EvaluationChild{Benchmark: "hd-harbor-bfcl", ExperimentID: "e_bad", Status: "done"}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b := rep.Benchmarks[0]
	if b.Arms["A"].Runs != 0 {
		t.Fatalf("invalid child counted %d runs", b.Arms["A"].Runs)
	}
	to := b.ThrownOut["A"]
	if to.Runs != 24 || len(to.Reasons) != 1 || rep.ThrownOutRuns != 24 {
		t.Fatalf("thrown out %+v, report total %d", to, rep.ThrownOutRuns)
	}
}

// N215 (fixture shaped like e_eval_..._5rcc): a score evaluation with the
// failover arm B, invalid only because model_fidelity fired in 7 of 12 runs
// of arm B. Arm A's 12 runs count; B's fired runs stay out of the failover
// section, which is still shown.
func TestReportCardGateOnlyKeepsCleanArm(t *testing.T) {
	src := newGateSource()
	out := schemas.GenericProbePrimaryOutage
	var runs []fixRun
	tasks := []string{"t1", "t2", "t3"}
	for _, task := range tasks {
		runs = append(runs, pass("A", task, 1, nil))
		runs = append(runs, fixRun{arm: "A", task: task, cond: out, outcome: schemas.OutcomeFail})
		runs = append(runs, fixRun{arm: "A", task: task, cond: "heimdall-model-down", outcome: schemas.OutcomePass})
		runs = append(runs, fixRun{arm: "A", task: task, cond: "heimdall-model-flaky", outcome: schemas.OutcomePass})
	}
	for _, task := range tasks {
		runs = append(runs, pass("B", task, 1, nil))
		runs = append(runs, fixRun{arm: "B", task: task, cond: out, outcome: schemas.OutcomePass})
		runs = append(runs, fixRun{arm: "B", task: task, cond: "heimdall-model-down", outcome: schemas.OutcomePass})
		runs = append(runs, fixRun{arm: "B", task: task, cond: "heimdall-model-flaky", outcome: schemas.OutcomePass})
	}
	src.add("e_5rcc", "hd-harbor-bfcl", []string{"A", "B"}, runs)
	src.manifests["e_5rcc"].Arms["B"] = schemas.ArmManifest{Label: "codex v1 on gpt-6-luna via openai" + failover.ArmLabelSuffix}
	src.fire("e_5rcc", "B", "heimdall-model-down", "model_fidelity", 3)
	src.fire("e_5rcc", "B", "heimdall-model-flaky", "model_fidelity", 3)
	src.fire("e_5rcc", "B", out, "model_fidelity", 1)
	src.decisions["e_5rcc"] = schemas.DecisionInvalid
	src.scope["e_5rcc"] = "runs"
	src.reasons["e_5rcc"] = []string{"gate model_fidelity fired in 7 of 12 runs (58.3%) of arm B, above 5%"}
	ev := scoreEval("done", schemas.EvaluationChild{Benchmark: "hd-harbor-bfcl", ExperimentID: "e_5rcc", Status: "done"})
	ev.Spec.Failover = &schemas.FailoverOption{Enabled: true}
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Benchmarks[0].Arms["A"]; got.Runs != 3 || got.Score != 1 {
		t.Fatalf("clean arm A row %+v", got)
	}
	if tot := rep.Totals["A"]; tot.Runs == 0 {
		t.Fatalf("arm A totals %+v", tot)
	}
	if rep.ThrownOutRuns != 0 {
		t.Fatalf("arm A had no fired gate, thrown out %d", rep.ThrownOutRuns)
	}
	if rep.Failover == nil || !rep.Failover.Clean.TaskSuccess || !rep.Failover.SuccessWith {
		t.Fatalf("failover section %+v", rep.Failover)
	}
}

// N215 with the gate on the scored arm: those runs are thrown out with the
// gate named, the others count.
func TestReportCardGateOnlyThrowsOutFiredRuns(t *testing.T) {
	src := newGateSource()
	src.add("e_g", "s", []string{"A"}, []fixRun{pass("A", "t1", 1, nil), pass("A", "t2", 1, nil), fail("A", "t3", 1, nil)})
	src.fire("e_g", "A", "", "model_fidelity", 1)
	src.decisions["e_g"] = schemas.DecisionInvalid
	src.scope["e_g"] = "runs"
	rep, err := Build(context.Background(), src, scoreEval("done", schemas.EvaluationChild{Benchmark: "s", ExperimentID: "e_g", Status: "done"}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b := rep.Benchmarks[0]
	if b.Arms["A"].Runs != 2 || b.ThrownOut["A"].Runs != 1 || b.ThrownOut["A"].Checks["model_fidelity"] != 1 || rep.ThrownOutRuns != 1 {
		t.Fatalf("row %+v thrown out %+v", b.Arms["A"], b.ThrownOut)
	}
}

// N182: a child still running keeps its runs whatever its stored decision
// (an analysis written while it ran is invalid by coverage).
func TestReadChildKeepsRunsWhileRunning(t *testing.T) {
	src := newGateSource()
	src.add("e_run", "s", []string{"A"}, []fixRun{pass("A", "t1", 1, nil), fail("A", "t2", 1, nil)})
	src.decisions["e_run"] = schemas.DecisionInvalid
	src.scope["e_run"] = "experiment"
	rep, err := Build(context.Background(), src, scoreEval("running", schemas.EvaluationChild{Benchmark: "s", ExperimentID: "e_run", Status: "running"}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Benchmarks[0].Arms["A"]; got.Runs != 2 || rep.ThrownOutRuns != 0 {
		t.Fatalf("running child row %+v thrown out %d", got, rep.ThrownOutRuns)
	}
}

// N182: planned runs are the children's counted slots of the shown arms;
// done runs are the slots with a finished run.
func TestReportPlannedRuns(t *testing.T) {
	src := newGateSource()
	src.add("e_p", "s", []string{"A"}, []fixRun{pass("A", "t1", 1, nil), fail("A", "t2", 1, nil)})
	m := src.manifests["e_p"]
	for _, task := range []string{"t1", "t2", "t3", "t4", "t5", "t6"} {
		m.Slots = append(m.Slots, schemas.Slot{SlotID: "e_p-A-" + task, Task: task, Arm: "A", Condition: schemas.ConditionClean, Trial: 1, Counted: true})
	}
	m.Slots = append(m.Slots, schemas.Slot{SlotID: "compat", Arm: "compat", Counted: false})
	rep, err := Build(context.Background(), src, scoreEval("running", schemas.EvaluationChild{Benchmark: "s", ExperimentID: "e_p", Status: "running"}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.PlannedRuns != 6 || rep.DoneRuns != 2 {
		t.Fatalf("planned %d done %d, want 6 and 2", rep.PlannedRuns, rep.DoneRuns)
	}
}
