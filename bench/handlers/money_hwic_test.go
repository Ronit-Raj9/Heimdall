package handlers

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/idempotency"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Money review of the merged lanes H13, H14 and H16 (fix wave 3, group C):
// one handler-level test per budget rule. The scheduler side of each rule is
// in engine/money_hwic_test.go.

// plannedBudgets is every planned experiment's budget, by experiment id.
func plannedBudgets(l *evalLauncher) map[string]schemas.BudgetSpec {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]schemas.BudgetSpec{}
	for id, e := range l.planned {
		out[id] = e.Budget
	}
	return out
}

// Rule 1: a budget below $0.01 per benchmark is refused at create with 400
// and the plain sentence, and nothing is saved or planned.
func TestMoneyBudgetBelowFloorRefusedNothingSaved(t *testing.T) {
	l := &evalLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1"})
	got := evalCall(r, "POST", APIPrefix+"/evaluations", `{`+oneArm+`,"benchmarks":["longmemeval","hd-harbor-tb2","hd-swe-verified"],"tasks_per_benchmark":1,"budget_usd":0.029}`)
	body := string(got.Response.Body())
	if got.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("status %d: %s", got.Response.StatusCode(), body)
	}
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(got.Response.Body(), &e)
	want := "The budget must be at least $0.01 per benchmark: at least $0.03 for 3 benchmarks."
	if e.Error.Message != want || strings.Contains(body, "quickrun") {
		t.Fatalf("refusal is not the plain sentence %q: %s", want, body)
	}
	if list, err := evalstore.Open(dir).List(); err != nil || len(list) != 0 {
		t.Fatalf("a refused create saved %d evaluations (%v)", len(list), err)
	}
	if n := len(plannedBudgets(l)); n != 0 {
		t.Fatalf("a refused create planned %d experiments", n)
	}
}

// Rule 2 and rule 3: with a known price and a budget that fits two runs of
// three, the child's cap admits exactly two (its expected run cost is the
// estimate); the scheduler's budget stop shows on the evaluation with the
// next run's cost (the page then offers "Raise budget and continue"); a
// raised budget continues the child under a cap that admits the third.
func TestMoneyBudgetFitsTwoOfThreeThenRaise(t *testing.T) {
	old := evalRunCostSource
	evalRunCostSource = func(OrgEnv) ForecastSource { return &fakeForecast{} }
	t.Cleanup(func() { evalRunCostSource = old })

	l := &stopLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1"})
	create := func(budget float64) (*schemas.Evaluation, schemas.BudgetSpec) {
		t.Helper()
		before := plannedBudgets(&l.evalLauncher)
		body := `{` + oneArm + `,"benchmarks":["longmemeval"],"tasks_per_benchmark":3,"trials":1,"budget_usd":` + jsonNum(budget) + `}`
		ctx := evalCall(r, "POST", APIPrefix+"/evaluations", body)
		if c := ctx.Response.StatusCode(); c < 200 || c > 299 {
			t.Fatalf("create with %v: %d %s", budget, c, ctx.Response.Body())
		}
		ev := decodeEval(t, ctx)
		for id, b := range plannedBudgets(&l.evalLauncher) {
			if _, seen := before[id]; !seen {
				return ev, b
			}
		}
		t.Fatalf("create planned nothing: %+v", ev)
		return nil, schemas.BudgetSpec{}
	}
	// The expected cost of one run comes from the estimate, whatever the budget.
	_, probe := create(100)
	run := probe.ReserveUSD
	if run <= 0 {
		t.Fatalf("a known price gave no expected run cost: %+v", probe)
	}
	budget := math.Floor(2.5*run*1e6) / 1e6
	ev, b := create(budget)
	if math.Abs(b.TotalUSD-budget) > 1e-6 || b.ReserveUSD != run {
		t.Fatalf("child budget %+v, want cap %v with %v per run", b, budget, run)
	}
	if fit := int(math.Floor((b.TotalUSD + 1e-9) / b.ReserveUSD)); fit != 2 {
		t.Fatalf("cap %v admits %d runs of %v, want 2 of 3", b.TotalUSD, fit, b.ReserveUSD)
	}

	// The scheduler stopped before the third run (engine side:
	// TestMoneyTwoOfThreeRunsThenRaise); the evaluation shows the stop.
	l.stopped, l.next = true, run
	st := evalstore.Open(dir)
	saved, err := st.Load(ev.ID)
	if err != nil || len(saved.Children) != 1 {
		t.Fatalf("saved %+v (%v)", saved, err)
	}
	m := &schemas.Manifest{Experiment: schemas.Experiment{Budget: b}}
	status := schemas.ExperimentStatus{State: schemas.ExperimentFailed, Slots: schemas.SlotProgress{Total: 3, Done: 2},
		SpentUSD: schemas.Observed(2*run, schemas.EvidenceGateway)}
	h := &evaluationsHandler{env: OrgEnv{Services: Services{Launcher: l}}}
	c := &saved.Children[0]
	c.Status = string(schemas.ExperimentFailed)
	h.budgetFacts(c, m, status)
	if err := st.Save(saved); err != nil {
		t.Fatal(err)
	}
	got := evalCall(r, "GET", APIPrefix+"/evaluations/"+ev.ID, "")
	var view struct {
		Status   string `json:"status"`
		Children []struct {
			BudgetStopped bool    `json:"budget_stopped"`
			NotStarted    int     `json:"not_started"`
			NextRunUSD    float64 `json:"next_run_usd"`
		} `json:"children"`
	}
	if err := json.Unmarshal(got.Response.Body(), &view); err != nil || len(view.Children) != 1 {
		t.Fatalf("GET %s (%v)", got.Response.Body(), err)
	}
	if ch := view.Children[0]; !ch.BudgetStopped || ch.NotStarted != 1 || ch.NextRunUSD != run {
		t.Fatalf("evaluation does not show the budget stop with the next run's cost: %s", got.Response.Body())
	}

	// Raise budget and continue: the stopped child is continued under a cap
	// that admits the third run.
	raised := math.Floor(3.5*run*1e6) / 1e6
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/raise-budget", `{"budget_usd":`+jsonNum(raised)+`}`)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("raise %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	exp := saved.Children[0].ExperimentID
	l.bmu.Lock()
	newCap, resumed := l.caps[exp], append([]string(nil), l.resumed...)
	l.bmu.Unlock()
	if len(resumed) != 1 || resumed[0] != exp {
		t.Fatalf("raise did not continue the stopped child: resumed %v", resumed)
	}
	if newCap < 3*run-1e-9 {
		t.Fatalf("raised cap %v does not admit a third run of %v", newCap, run)
	}
}

// Rule 4 (handler side): with an unknown price the child's expected run cost
// is the budget split evenly over its runs, never rounded up, so every
// planned run fits the cap (before, $5 over 3 runs reserved $1.67 a run and
// the third run, 3 x 1.67 = 5.01, was refused by the scheduler). With no
// budget split at all (reserve 0) the scheduler runs one at a time:
// engine/money_hwic_test.go TestMoneyUnknownPriceOneAtATime.
func TestMoneyUnknownPriceEvenSplitFitsEveryRun(t *testing.T) {
	for _, tc := range []struct {
		budget float64
		runs   int
	}{{5, 3}, {0.05, 3}, {0.01, 3}, {1, 7}, {10, 6}} {
		r := quickReserve(tc.budget, tc.runs)
		if r <= 0 || float64(tc.runs)*r > tc.budget+1e-9 {
			t.Errorf("quickReserve(%v, %d) = %v: %d runs need %v, over the cap", tc.budget, tc.runs, r, tc.runs, float64(tc.runs)*r)
		}
	}
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1"})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations", `{`+oneArm+`,"benchmarks":["longmemeval"],"tasks_per_benchmark":3,"trials":1,"budget_usd":5}`)
	if c := ctx.Response.StatusCode(); c < 200 || c > 299 {
		t.Fatalf("create %d %s", c, ctx.Response.Body())
	}
	for _, b := range plannedBudgets(l) {
		if b.TotalUSD != 5 || 3*b.ReserveUSD > 5+1e-9 {
			t.Fatalf("unpriced child budget %+v: three runs do not fit", b)
		}
	}
}

// Rule 5: the async create answers 202 before any run starts, and a retry
// with the same Idempotency-Key gets the same evaluation without starting a
// second one.
func TestMoneyAsyncCreateIdempotentRetry(t *testing.T) {
	inBackground(t)
	gate := make(chan struct{})
	l := &gatedLauncher{gates: map[string]chan struct{}{"longmemeval": gate}}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1"})
	mw := idempotency.Middleware(schemas.ModeRecorded, idempotency.NewStore())
	h := mw(func(ctx *fasthttp.RequestCtx) { r.Handler(ctx) })
	body := `{` + oneArm + `,"benchmarks":["longmemeval"],"tasks_per_benchmark":2,"budget_usd":1}`
	post := func() *fasthttp.RequestCtx {
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod("POST")
		ctx.Request.SetRequestURI(APIPrefix + "/evaluations")
		ctx.Request.Header.SetContentType("application/json")
		ctx.Request.Header.Set(idempotency.Header, "money-review-key-0001")
		ctx.Request.SetBodyString(body)
		auth.SetPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, UserID: "u1", OrgID: "org1", Role: auth.RoleMember})
		h(ctx)
		return ctx
	}
	done := make(chan *fasthttp.RequestCtx, 1)
	go func() { done <- post() }()
	var first *fasthttp.RequestCtx
	select {
	case first = <-done:
	case <-time.After(10 * time.Second):
		close(gate)
		t.Fatal("create waited for a run to start")
	}
	if first.Response.StatusCode() != fasthttp.StatusAccepted {
		close(gate)
		t.Fatalf("create %d %s", first.Response.StatusCode(), first.Response.Body())
	}
	ev := decodeEval(t, first)
	l.mu.Lock()
	startedBefore := len(l.started)
	l.mu.Unlock()
	if ev.Status != schemas.EvaluationQueued || startedBefore != 0 {
		close(gate)
		t.Fatalf("answered %q with %d runs started, want queued with none", ev.Status, startedBefore)
	}
	retry := post()
	if retry.Response.StatusCode() != fasthttp.StatusAccepted || string(retry.Response.Header.Peek(idempotency.ReplayedHeader)) != "true" {
		close(gate)
		t.Fatalf("retry %d %s", retry.Response.StatusCode(), retry.Response.Body())
	}
	if again := decodeEval(t, retry); again.ID != ev.ID {
		close(gate)
		t.Fatalf("retry made evaluation %s, first was %s", again.ID, ev.ID)
	}
	close(gate)
	waitStored(t, dir, ev.ID, func(e *schemas.Evaluation) bool { return e.Status != schemas.EvaluationQueued })
	if list, err := evalstore.Open(dir).List(); err != nil || len(list) != 1 {
		t.Fatalf("evaluations saved: %d (%v), want 1", len(list), err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.planned) != 1 || len(l.started) != 1 {
		t.Fatalf("planned %d, started %v: want one child started once", len(l.planned), l.started)
	}
}

func jsonNum(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
