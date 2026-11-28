package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// rerunEvalServe posts body to /evaluations/{id}/rerun-failures of a
// finished evaluation whose one benchmark (testExp) has a failed run, and
// returns the status, the body and the experiments the launcher planned.
func rerunEvalServe(t *testing.T, body string) (int, string, *evalLauncher) {
	t.Helper()
	root := t.TempDir()
	ev := &schemas.Evaluation{ID: "ev_20261128_000000_abc123", Status: schemas.EvaluationDone, CreatedAt: time.Now(),
		Spec:     schemas.EvaluationSpec{Name: "nightly", Benchmarks: []string{"s"}, Trials: 1, BudgetUSD: 5},
		Children: []schemas.EvaluationChild{{Benchmark: "s", ExperimentID: testExp, Status: "done", BudgetUSD: 5}}}
	if err := evalstore.Open(root).Save(ev); err != nil {
		t.Fatal(err)
	}
	l := &evalLauncher{}
	h := &evaluationsHandler{env: OrgEnv{Mode: "local", Cfg: &lib.Config{Root: root}, Services: Services{Launcher: l, Index: testIndex()}}, now: time.Now}
	r := router.New()
	r.POST(APIPrefix+"/evaluations/{id}/rerun-failures", h.rerunFailures)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(APIPrefix + "/evaluations/" + ev.ID + "/rerun-failures")
	ctx.Request.SetBodyString(body)
	r.Handler(&ctx)
	return ctx.Response.StatusCode(), string(ctx.Response.Body()), l
}

// TestEvaluationRerunTakesTheWebCap: the body Rerun failures sends
// (components/evaluations/paidCaps.ts) caps the rerun; round 3 had no cap
// and the rerun ran under the parent's whole budget (PAID-6).
func TestEvaluationRerunTakesTheWebCap(t *testing.T) {
	code, body, l := rerunEvalServe(t, `{"budget_usd":0.3}`)
	if code != fasthttp.StatusAccepted {
		t.Fatalf("%d %s", code, body)
	}
	if len(l.planned) != 1 {
		t.Fatalf("planned %v", l.planned)
	}
	for _, exp := range l.planned {
		if exp.Budget.TotalUSD != 0.3 || exp.Budget.ReserveUSD > 0.3 {
			t.Fatalf("rerun budget %+v, want 0.3", exp.Budget)
		}
	}
	if !strings.Contains(body, `"budget_usd":0.3`) {
		t.Fatalf("the new evaluation names its cap: %s", body)
	}
}

func TestEvaluationRerunRefusesBadCap(t *testing.T) {
	code, body, l := rerunEvalServe(t, `{"budget_usd":-1}`)
	if code != fasthttp.StatusBadRequest || len(l.planned) != 0 {
		t.Fatalf("%d %s", code, body)
	}
}
