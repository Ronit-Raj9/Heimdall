package handlers

import (
	"encoding/json"
	"testing"

	"github.com/valyala/fasthttp"
)

// plannedAndEstimated posts body to the launch and to the estimate and
// returns the runs the launch planned (every arm under every condition,
// trials times tasks) and the estimate's answer.
func plannedAndEstimated(t *testing.T, body string) (int, EvalEstimate) {
	t.Helper()
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations", body)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("launch status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	planned := 0
	for _, c := range ev.Children {
		exp := l.planned[c.ExperimentID]
		if exp == nil {
			t.Fatalf("no experiment planned for %s", c.Benchmark)
		}
		for _, cond := range exp.Conditions {
			planned += len(cond.Tasks.IDs) * cond.Trials * len(exp.Arms)
		}
	}
	ctx = evalCall(r, "POST", APIPrefix+"/evaluations/estimate", body)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("estimate status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var est EvalEstimate
	if err := json.Unmarshal(ctx.Response.Body(), &est); err != nil {
		t.Fatal(err)
	}
	return planned, est
}

// breakdown is the form's line: the sum over benchmarks of
// arms x conditions x trials x tasks must be the estimate's own Runs.
func breakdown(est EvalEstimate) int {
	n := 0
	for _, b := range est.PerBenchmark {
		if b.Skipped == "" {
			n += est.Arms * len(b.Conditions) * est.Trials * b.Tasks
		}
	}
	return n
}

// The exact body the Failure tests form builds with a fallback model
// (failureTestSpec): the estimate's runs are the runs the launch plans.
func TestFailureTestsFallbackRunsMatchEstimate(t *testing.T) {
	body := `{"name":"Failure test: scripted","mode":"score","arms":[{"label":"A","agent":"scripted","model":"gpt-4o","provider":"openai"}],
		"benchmarks":["longmemeval"],"tasks_per_benchmark":3,"trials":1,"faults":["clean","model-down","broken-tool","misleading-instruction"],
		"failover":{"enabled":true,"fallbacks":[{"provider":"openai","model":"gpt-4o-mini"}]}}`
	planned, est := plannedAndEstimated(t, body)
	if planned == 0 || est.Runs != planned {
		t.Fatalf("estimate %d runs, launch plans %d", est.Runs, planned)
	}
	if est.Arms != 2 || est.Trials != 1 || breakdown(est) != est.Runs {
		t.Fatalf("breakdown arms %d trials %d gives %d, runs %d", est.Arms, est.Trials, breakdown(est), est.Runs)
	}
}

// The case seen on the live site: a failover evaluation with 3 tasks. The
// estimate says 3 tasks x 1 try x 2 conditions x 2 setups = 12, and the
// launch plans 12.
func TestFailoverThreeTasksEstimateEqualsPlanned(t *testing.T) {
	body := `{"mode":"score","arms":[{"label":"A","agent":"scripted","model":"openai/gpt-4o"}],"benchmarks":["longmemeval"],
		"tasks_per_benchmark":3,"trials":1,"failover":{"enabled":true,"fallbacks":[{"model":"openai/gpt-4o-mini"}]}}`
	planned, est := plannedAndEstimated(t, body)
	if planned != 12 || est.Runs != 12 || breakdown(est) != 12 {
		t.Fatalf("planned %d estimate %d breakdown %d, want 12", planned, est.Runs, breakdown(est))
	}
	if b := est.PerBenchmark[0]; b.Tasks != 3 || len(b.Conditions) != 2 || est.Arms != 2 {
		t.Fatalf("per benchmark %+v arms %d", b, est.Arms)
	}
}
