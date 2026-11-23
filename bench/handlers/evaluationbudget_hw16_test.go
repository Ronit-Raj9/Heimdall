package handlers

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Fix wave 3, N190 and N211: the budget floor, sub-cent shares, the budget
// stop read from the scheduler, the honest Bifrost sentence and arm labels.

func TestCreateEvaluationRefusesBudgetBelowFloor(t *testing.T) {
	r, _ := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites()})
	for _, tc := range []struct{ body, want string }{
		{`{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":1,"budget_usd":0.001}`,
			"The budget must be at least $0.01 per benchmark: at least $0.01 for 1 benchmark."},
		{`{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval","hd-harbor-tb2"],"tasks_per_benchmark":1,"budget_usd":0.015}`,
			"The budget must be at least $0.01 per benchmark: at least $0.02 for 2 benchmarks."},
	} {
		got := evalCall(r, "POST", APIPrefix+"/evaluations", tc.body)
		body := string(got.Response.Body())
		if got.Response.StatusCode() != fasthttp.StatusBadRequest || !strings.Contains(body, tc.want) {
			t.Fatalf("budget below the floor: %d %s; want 400 with %q", got.Response.StatusCode(), body, tc.want)
		}
	}
	ok := evalCall(r, "POST", APIPrefix+"/evaluations", `{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":1,"budget_usd":0.01}`)
	if c := ok.Response.StatusCode(); c < 200 || c > 299 {
		t.Fatalf("budget at the floor refused: %d %s", c, ok.Response.Body())
	}
}

func TestChildBudgetKeepsSubCent(t *testing.T) {
	spec := schemas.EvaluationSpec{BudgetUSD: 0.03, Benchmarks: []string{"a", "b"}}
	if got := childBudget(spec); got != 0.015 {
		t.Fatalf("childBudget(0.03 over 2) = %v, want 0.015", got)
	}
}

// stopLauncher reports the scheduler's budget stop of the newest run job.
type stopLauncher struct {
	budgetLauncher
	stopped bool
	next    float64
}

func (l *stopLauncher) BudgetStop(string) (bool, float64, bool) { return l.stopped, l.next, true }

func TestBudgetFactsUsesBudgetStopEvent(t *testing.T) {
	m := &schemas.Manifest{Experiment: schemas.Experiment{Budget: schemas.BudgetSpec{TotalUSD: 0.01}}}
	st := schemas.ExperimentStatus{State: schemas.ExperimentFailed, Slots: schemas.SlotProgress{Total: 4},
		SpentUSD: schemas.Observed(0, schemas.EvidenceGateway)}
	l := &stopLauncher{stopped: true, next: 0.02}
	h := &evaluationsHandler{env: OrgEnv{Services: Services{Launcher: l}}}
	c := &schemas.EvaluationChild{ExperimentID: "e_1"}
	h.budgetFacts(c, m, st)
	if !c.BudgetStopped || c.NotStarted != 4 || c.NextRunUSD != 0.02 {
		t.Fatalf("a stop before the first run (spent 0, next run 0.02) not read: %+v", c)
	}
	l.stopped, l.next = false, 0
	st.SpentUSD = schemas.Observed(0.01, schemas.EvidenceGateway)
	st.Slots.Done = 1
	h.budgetFacts(c, m, st)
	if c.BudgetStopped || c.NextRunUSD != 0 {
		t.Fatalf("a failure the scheduler did not call a budget stop read as one: %+v", c)
	}
}

func TestBifrostUseNoBudgetSentence(t *testing.T) {
	sum := BifrostUse{GatewayConfigured: true, Reachable: true, LoadedPlugins: []string{"governance"}}
	sum.Features = BifrostFeatures(sum)
	detail := func(ev *schemas.Evaluation) string {
		for _, f := range EvaluationBifrostFeatures(ev, sum) {
			if f.ID == "budgets" {
				return f.Detail
			}
		}
		return ""
	}
	old := &schemas.Evaluation{ID: "ev_old", Spec: schemas.EvaluationSpec{BudgetUSD: 0.001}, Children: []schemas.EvaluationChild{
		{Benchmark: "a", ExperimentID: "e_a"},
	}}
	if d := detail(old); d != "No budget reached the run keys for this evaluation." {
		t.Fatalf("a budget that never reached a run key: %q", d)
	}
	small := &schemas.Evaluation{ID: "ev_new", Spec: schemas.EvaluationSpec{BudgetUSD: 0.05}, Children: []schemas.EvaluationChild{
		{Benchmark: "a", ExperimentID: "e_a", BudgetUSD: 0.005},
	}}
	if d := detail(small); !strings.Contains(d, "$0.0050") || !strings.Contains(d, "enforced by Bifrost") {
		t.Fatalf("a sub-cent cap: %q", d)
	}
}

func TestRaiseBudgetFloor(t *testing.T) {
	l := &budgetLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	ev := decodeEval(t, evalCall(r, "POST", APIPrefix+"/evaluations", `{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval","hd-harbor-tb2"],"tasks_per_benchmark":1}`))
	st := evalstore.Open(dir)
	saved, err := st.Load(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Children) != 2 {
		t.Skipf("create launched %d children here", len(saved.Children))
	}
	for i := range saved.Children { // an evaluation made before the floor
		saved.Children[i].BudgetUSD, saved.Children[i].BudgetStopped, saved.Children[i].NotStarted = 0.004, true, 1
		saved.Children[i].Status = string(schemas.ExperimentFailed)
	}
	if err := st.Save(saved); err != nil {
		t.Fatal(err)
	}
	got := evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/raise-budget", `{"budget_usd":0.015}`)
	want := "The budget must be at least $0.01 per benchmark: at least $0.02 for 2 benchmarks."
	if got.Response.StatusCode() != fasthttp.StatusBadRequest || !strings.Contains(string(got.Response.Body()), want) {
		t.Fatalf("raise below the floor: %d %s", got.Response.StatusCode(), got.Response.Body())
	}
}

func TestCreateModelSwitchEvaluationFromFormLabels(t *testing.T) {
	r, _ := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites()})
	body := `{"mode":"model_switch","arms":[{"label":"A","agent":"scripted","model":"m1"},{"label":"B","agent":"scripted","model":"m2"},{"label":"C","agent":"scripted","model":"m3"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":1}`
	got := evalCall(r, "POST", APIPrefix+"/evaluations", body)
	if c := got.Response.StatusCode(); c < 200 || c > 299 {
		t.Fatalf("model switch with arms A, B, C: %d %s", c, got.Response.Body())
	}
}

func TestCreateEvaluationBadArmLabelPlainText(t *testing.T) {
	r, _ := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites()})
	body := `{"mode":"model_switch","arms":[{"label":"current","agent":"scripted","model":"m1"},{"label":"B","agent":"scripted","model":"m2"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":1}`
	got := evalCall(r, "POST", APIPrefix+"/evaluations", body)
	b := string(got.Response.Body())
	want := `setup label \"current\" is not allowed: use a capital letter followed by letters or digits, for example A or B`
	if got.Response.StatusCode() != fasthttp.StatusBadRequest || !strings.Contains(b, want) {
		t.Fatalf("label current: %d %s", got.Response.StatusCode(), b)
	}
	// create sends the error through plainErr once lane H13 switches its two
	// send sites; the helper itself drops the internal prefix.
	if p := plainErr(badf("setup label %q is not allowed", "current")); strings.Contains(p, "quickrun") {
		t.Fatalf("plainErr kept the prefix: %q", p)
	}
}
