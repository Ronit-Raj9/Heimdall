package handlers

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// K5 (PQ1-5): the page's Resume (POST /evaluations/{id}/resume, no body) on
// an evaluation with a cap goes through and every child keeps its cap.
func TestResumeWebBodyKeepsCap(t *testing.T) {
	l := &pausingLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	created := evalCall(r, "POST", APIPrefix+"/evaluations", `{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":1,"budget_usd":0.05}`)
	if created.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("create %d: %s", created.Response.StatusCode(), created.Response.Body())
	}
	ev := decodeEval(t, created)
	l.paused.Store(true)
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/resume", "")
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("resume %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	got := decodeEval(t, ctx)
	for _, c := range got.Children {
		if c.BudgetUSD <= 0 || c.BudgetUSD > 0.05 {
			t.Fatalf("child %s cap after resume = %v, want its share of 0.05", c.Benchmark, c.BudgetUSD)
		}
	}
}

// resumeCapProblem refuses a resume with no cap or a cap used up, in plain words.
func TestResumeCapProblem(t *testing.T) {
	ev := &schemas.Evaluation{}
	kid := func(b, s float64) []*schemas.EvaluationChild {
		return []*schemas.EvaluationChild{{Benchmark: "a", BudgetUSD: b, SpentUSD: s}}
	}
	if msg := resumeCapProblem(ev, kid(0, 0)); !strings.Contains(msg, "no spend cap") {
		t.Fatalf("no cap: %q", msg)
	}
	if msg := resumeCapProblem(ev, kid(0.05, 0.05)); !strings.Contains(msg, "used up") {
		t.Fatalf("used up: %q", msg)
	}
	if msg := resumeCapProblem(ev, kid(0.05, 0.01)); msg != "" {
		t.Fatalf("cap left: %q", msg)
	}
}
