package reportcard

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestTotalsReportJudgeCostApart: a judged benchmark's judge spend
// (judge_cost_usd) is reported per arm, apart from the agent's cost; it is
// nil (not a partial sum) when a judged run's judge cost is unknown, and
// absent outside a judged benchmark (round 4, F5, PH3-2).
func TestTotalsReportJudgeCostApart(t *testing.T) {
	run := func(task string, cost, judge schemas.Value) graded {
		return graded{task: task, condition: schemas.ConditionClean, metrics: map[string]schemas.Value{metricCostUSD: cost, metricJudgeCostUSD: judge}}
	}
	obs := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }
	none := func(childRuns, string) string { return "" }
	priced := []childRuns{{child: schemas.EvaluationChild{Benchmark: "bigfinancebench"}, arms: map[string][]graded{
		"A": {run("t1", obs(0.0005), obs(0.002)), run("t2", obs(0.0005), obs(0.003))},
	}}}
	a := totals(priced, []string{"A"}, none)["A"]
	if a.JudgeRuns != 2 || a.JudgeCostRuns != 2 || a.JudgeCostUSD == nil || *a.JudgeCostUSD != 0.005 || a.CostUSD == nil || *a.CostUSD != 0.001 {
		t.Fatalf("judged totals %+v", a)
	}
	unpriced := []childRuns{{child: schemas.EvaluationChild{Benchmark: "b"}, arms: map[string][]graded{
		"A": {run("t1", obs(0.0005), obs(0.002)), run("t2", obs(0.0005), schemas.Unknown("no usage", schemas.EvidenceGateway))},
	}}}
	if u := totals(unpriced, []string{"A"}, none)["A"]; u.JudgeRuns != 2 || u.JudgeCostRuns != 1 || u.JudgeCostUSD != nil {
		t.Fatalf("one unpriced judge call: %+v", u)
	}
	plain := []childRuns{{child: schemas.EvaluationChild{Benchmark: "c"}, arms: map[string][]graded{
		"A": {run("t1", obs(0.0005), schemas.NotApplicable())},
	}}}
	if p := totals(plain, []string{"A"}, none)["A"]; p.JudgeRuns != 0 || p.JudgeCostUSD != nil {
		t.Fatalf("not judged: %+v", p)
	}
}

// TestCheatEstRuns: the cheat check row says about how many runs a start
// makes, so its cap and confirm can name an amount (PH2-3): every arm once
// per twin, at most the evaluation's tasks per benchmark.
func TestCheatEstRuns(t *testing.T) {
	ev := &schemas.Evaluation{Spec: schemas.EvaluationSpec{Benchmarks: []string{"s@1"}, TasksPerBenchmark: 3,
		Arms: []schemas.EvaluationArm{{Label: "A"}, {Label: "B"}}}, Children: []schemas.EvaluationChild{{Benchmark: "s@1"}}}
	opt := Options{TwinCount: func(b string) int {
		if b == "s" {
			return 7
		}
		return 0
	}}
	if n := cheatEstRuns(ev, opt); n != 6 {
		t.Fatalf("est runs %d, want 2 arms x 3 tasks", n)
	}
	if n := cheatEstRuns(ev, Options{}); n != 0 {
		t.Fatalf("no twin count: %d", n)
	}
}
