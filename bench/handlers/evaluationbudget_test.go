package handlers

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// budgetLauncher is evalLauncher with the run controls a budget raise uses.
type budgetLauncher struct {
	evalLauncher
	bmu     sync.Mutex
	caps    map[string]float64
	resumed []string
}

func (l *budgetLauncher) Controls(context.Context, string) (*runctl.State, []runctl.Entry, error) {
	return &runctl.State{}, nil, nil
}
func (l *budgetLauncher) Pause(context.Context, string, string) (*runctl.State, error) {
	return &runctl.State{}, nil
}
func (l *budgetLauncher) Unpause(context.Context, string, string) (*runctl.State, bool, error) {
	return &runctl.State{}, false, nil
}
func (l *budgetLauncher) PatchControls(_ context.Context, id string, p runctl.Patch) (*runctl.State, error) {
	l.bmu.Lock()
	defer l.bmu.Unlock()
	if l.caps == nil {
		l.caps = map[string]float64{}
	}
	l.caps[id] = *p.TotalUSD
	return &runctl.State{}, nil
}
func (l *budgetLauncher) CancelRun(context.Context, string, string, string, string) (*runctl.State, error) {
	return &runctl.State{}, nil
}
func (l *budgetLauncher) RequeueRun(context.Context, string, string, string) (*runctl.State, error) {
	return &runctl.State{}, nil
}
func (l *budgetLauncher) Resume(_ context.Context, id string) error {
	l.bmu.Lock()
	defer l.bmu.Unlock()
	l.resumed = append(l.resumed, id)
	return nil
}

func estimateOf(per ...EvalBenchmarkEstimate) *EvalEstimate {
	est := &EvalEstimate{PerBenchmark: per, EstCostUSD: EvalCostRange{Known: true}}
	for _, b := range per {
		if b.Skipped == "" {
			est.EstCostUSD.Low += b.EstCostUSD.Low
			est.EstCostUSD.High += b.EstCostUSD.High
			est.EstCostUSD.Known = est.EstCostUSD.Known && b.EstCostUSD.Known
		}
	}
	return est
}

func TestEstimateBudget(t *testing.T) {
	t.Setenv(EvaluationBudgetMaxEnv, "")
	cost := func(lo, hi float64) EvalCostRange { return EvalCostRange{Low: lo, High: hi, Known: true} }
	two := estimateOf(
		EvalBenchmarkEstimate{Benchmark: "a", Runs: 16, EstCostUSD: cost(4, 16)},
		EvalBenchmarkEstimate{Benchmark: "b", Runs: 16, EstCostUSD: cost(2, 8)},
		EvalBenchmarkEstimate{Benchmark: "gone", Skipped: "no task"},
	)
	t.Run("default cap stops runs and says how many", func(t *testing.T) {
		b := estimateBudget(two, 0, 0)
		if !b.Default || b.PerBenchmarkUSD != 5 || b.BudgetUSD != 10 {
			t.Fatalf("default %+v", b)
		}
		// a: $1 a run at the high end, 5 of 16 fit; b: $0.50, 10 fit.
		if b.RunsSkippedHigh != 11+6 || b.RunsSkippedLow != 0 || b.Fit != BudgetMayExceed {
			t.Fatalf("skipped %+v", b)
		}
		if b.SuggestedUSD != 30 || b.MaxUSD != EvaluationBudgetMaxDefault { // ceil(24 * 1.25)
			t.Fatalf("suggested %+v", b)
		}
	})
	t.Run("a budget that covers the high estimate fits", func(t *testing.T) {
		b := estimateBudget(two, 40, 0)
		if b.Default || b.PerBenchmarkUSD != 20 || b.Fit != BudgetFits || b.RunsSkippedHigh != 0 {
			t.Fatalf("fits %+v", b)
		}
	})
	t.Run("a budget below even the low estimate exceeds", func(t *testing.T) {
		if b := estimateBudget(two, 2, 0); b.Fit != BudgetExceeds || b.RunsSkippedLow == 0 {
			t.Fatalf("exceeds %+v", b)
		}
	})
	t.Run("the suggestion is capped by the org maximum and says so", func(t *testing.T) {
		t.Setenv(EvaluationBudgetMaxEnv, "20")
		b := estimateBudget(two, 0, 0)
		if b.SuggestedUSD != 20 || b.MaxUSD != 20 || !strings.Contains(b.Note, "maximum") {
			t.Fatalf("capped %+v", b)
		}
		if b := estimateBudget(two, 0, 12); b.MaxUSD != 12 || b.SuggestedUSD != 12 {
			t.Fatalf("daily cap %+v", b)
		}
	})
	t.Run("unknown cost suggests the default and never guesses", func(t *testing.T) {
		est := estimateOf(EvalBenchmarkEstimate{Benchmark: "a", Runs: 10})
		est.EstCostUSD.Known = false
		b := estimateBudget(est, 0, 0)
		if b.Fit != BudgetUnknown || b.SuggestedUSD != 5 || b.RunsSkippedHigh != 0 || b.Note == "" {
			t.Fatalf("unknown %+v", b)
		}
	})
}

func TestRunsPast(t *testing.T) {
	for _, tc := range []struct {
		runs       int
		total, cap float64
		want       int
	}{
		{16, 16, 5, 11}, {16, 4, 5, 0}, {10, 0, 5, 0}, {0, 3, 1, 0}, {3, 3, 1, 2},
	} {
		if got := runsPast(tc.runs, tc.total, tc.cap); got != tc.want {
			t.Errorf("runsPast(%d, %v, %v) = %d, want %d", tc.runs, tc.total, tc.cap, got, tc.want)
		}
	}
}

func TestBudgetFacts(t *testing.T) {
	h := &evaluationsHandler{}
	m := &schemas.Manifest{Experiment: schemas.Experiment{Budget: schemas.BudgetSpec{TotalUSD: 5}}}
	status := func(state schemas.ExperimentState, total, done, running int, spent float64) schemas.ExperimentStatus {
		return schemas.ExperimentStatus{State: state, Slots: schemas.SlotProgress{Total: total, Done: done, Running: running},
			SpentUSD: schemas.Observed(spent, schemas.EvidenceGateway)}
	}
	t.Run("stopped at the cap", func(t *testing.T) {
		c := &schemas.EvaluationChild{}
		if !h.budgetFacts(c, m, status(schemas.ExperimentFailed, 16, 3, 0, 4.2)) {
			t.Fatal("no change reported")
		}
		if !c.BudgetStopped || c.NotStarted != 13 || c.BudgetUSD != 5 || c.SpentUSD != 4.2 {
			t.Fatalf("child %+v", c)
		}
		if h.budgetFacts(c, m, status(schemas.ExperimentFailed, 16, 3, 0, 4.2)) {
			t.Fatal("unchanged facts reported a change")
		}
	})
	t.Run("a crash far below the cap is not a budget stop", func(t *testing.T) {
		c := &schemas.EvaluationChild{}
		h.budgetFacts(c, m, status(schemas.ExperimentFailed, 16, 3, 0, 0.3))
		if c.BudgetStopped || c.NotStarted != 0 {
			t.Fatalf("child %+v", c)
		}
	})
	t.Run("still running is not stopped; a raised cap wins over the manifest", func(t *testing.T) {
		c := &schemas.EvaluationChild{BudgetUSD: 20}
		h.budgetFacts(c, m, status(schemas.ExperimentRunning, 16, 3, 2, 6))
		if c.BudgetStopped || c.BudgetUSD != 20 {
			t.Fatalf("child %+v", c)
		}
	})
}

func TestEvaluationRaiseBudget(t *testing.T) {
	l := &budgetLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	body := `{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval","hd-harbor-tb2"],"tasks_per_benchmark":1}`
	ev := decodeEval(t, evalCall(r, "POST", APIPrefix+"/evaluations", body))
	if len(ev.Children) != 2 || ev.Children[0].BudgetUSD != EvaluationDefaultBenchmarkBudget {
		t.Fatalf("children %+v", ev.Children)
	}
	// The first benchmark stopped at its cap with 3 runs left; the second runs.
	st := evalstore.Open(dir)
	saved, err := st.Load(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Children[0].Status, saved.Children[0].BudgetStopped, saved.Children[0].NotStarted, saved.Children[0].SpentUSD =
		string(schemas.ExperimentFailed), true, 3, 4.9
	if err := st.Save(saved); err != nil {
		t.Fatal(err)
	}
	if got := evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/raise-budget", `{"budget_usd":8}`); got.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("lower budget %d: %s", got.Response.StatusCode(), got.Response.Body())
	}
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/raise-budget", `{"budget_usd":30}`)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("raise %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	got := decodeEval(t, ctx)
	a, b := got.Children[0], got.Children[1]
	if a.BudgetStopped || a.NotStarted != 0 || a.Status != string(schemas.ExperimentRunning) || a.BudgetUSD != 15 || b.BudgetUSD != 15 || got.Spec.BudgetUSD != 30 {
		t.Fatalf("raised %+v", got)
	}
	if l.caps[a.ExperimentID] != 15 || l.caps[b.ExperimentID] != 15 || len(l.resumed) != 1 || l.resumed[0] != a.ExperimentID {
		t.Fatalf("caps %v resumed %v", l.caps, l.resumed)
	}
	if got := evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/raise-budget", `{"budget_usd":"x"}`); got.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("bad body %d", got.Response.StatusCode())
	}
}

func TestEvaluationRaiseBudgetNeedsRunControls(t *testing.T) {
	r, _ := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites()})
	ev := decodeEval(t, evalCall(r, "POST", APIPrefix+"/evaluations", `{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":1}`))
	if got := evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/raise-budget", `{"budget_usd":30}`); got.Response.StatusCode() != fasthttp.StatusServiceUnavailable {
		t.Fatalf("no run controls %d", got.Response.StatusCode())
	}
}

func TestEvaluationStatusBudgetStoppedIsDone(t *testing.T) {
	ev := &schemas.Evaluation{Status: schemas.EvaluationRunning, Children: []schemas.EvaluationChild{
		{Benchmark: "a", ExperimentID: "e1", Status: string(schemas.ExperimentFailed), BudgetStopped: true, NotStarted: 3},
		{Benchmark: "b", Status: schemas.EvaluationChildSkipped},
	}}
	if got := evaluationStatus(ev); got != schemas.EvaluationDone {
		t.Fatalf("budget stopped only: %s, want done (its finished runs make the report card)", got)
	}
	ev.Children[0].BudgetStopped = false
	if got := evaluationStatus(ev); got != schemas.EvaluationFailed {
		t.Fatalf("plain failure: %s", got)
	}
}
