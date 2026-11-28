package handlers

import (
	"context"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// historyForecast forecasts from sealed per-run costs of the arm's agent
// and model, like the server's forecast: the range is the runs times the
// lowest and highest past run. With no history it answers with library
// runs of other agents, a much wider range. history can grow between asks,
// as runs seal.
type historyForecast struct {
	mu      sync.Mutex
	history []float64
}

func (f *historyForecast) Priced(string) bool { return true }

func (f *historyForecast) Matrix(context.Context, string, leaderboard.MatrixQuery) (*schemas.ModelMatrix, error) {
	return nil, schemas.ErrNotFound
}

func (f *historyForecast) Estimate(_ context.Context, exp *schemas.Experiment) (*schemas.Forecast, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	runs := 0
	for _, c := range exp.Conditions {
		runs += len(c.Tasks.IDs) * max(1, c.Trials)
	}
	lo, hi, basis := 0.036, 0.236, schemas.BasisLibrary
	if len(f.history) > 0 {
		lo, hi, basis = f.history[0], f.history[0], schemas.BasisAgentModel
		for _, c := range f.history {
			lo, hi = min(lo, c), max(hi, c)
		}
	}
	l, h, s1, s2 := lo*float64(runs), hi*float64(runs), 60.0*float64(runs), 120.0*float64(runs)
	out := &schemas.Forecast{Suite: exp.Suite, Concurrency: 1}
	for id := range exp.Arms {
		out.Arms = append(out.Arms, schemas.ArmForecast{Arm: id, Basis: basis,
			CostUSD:    schemas.ForecastRange{Kind: schemas.RangeHistory, Low: &l, High: &h},
			AgentTimeS: schemas.ForecastRange{Kind: schemas.RangeHistory, Low: &s1, High: &s2}})
	}
	return out, nil
}

func sweVerifiedSuites() *fakeSuites {
	ids := []string{"t01", "t02", "t03", "t04", "t05", "t06", "t07", "t08", "t09", "t10", "t11", "t12"}
	st := &schemas.Suite{ID: "hd-swe-verified", Version: "1", TaskIDs: ids}
	for _, id := range ids {
		st.Tasks = append(st.Tasks, &schemas.Task{ID: id})
	}
	return &fakeSuites{ids: []string{"hd-swe-verified"}, suites: map[string]*schemas.Suite{"hd-swe-verified": st}}
}

// observedRequest is the wizard's draft seen live: codex on gpt-6-luna,
// SWE-bench Verified, 10 tasks once each.
func observedRequest(budget float64, presets bool) *EvalEstimateRequest {
	return &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-6-luna"}}, Benchmarks: []string{"hd-swe-verified"},
		TasksPerBenchmark: 10, Trials: 1, Faults: []string{"clean"}, BudgetUSD: budget, Presets: presets}
}

// The step 4 presets request (no budget) and the step 5 request (the seeded
// budget) are one estimate: one cost range, the Quick look card included,
// even when a run seals between the two asks.
func TestEstimateObservedDraftIsOneRange(t *testing.T) {
	src := &historyForecast{history: []float64{0.004, 0.006, 0.007, 0.009, 0.01}}
	cache := newForecastCache()
	ask := func(req *EvalEstimateRequest) *EvalEstimate {
		est, err := estimateEvaluation(context.Background(), sweVerifiedSuites(), src, req, nil, "", cache)
		if err != nil {
			t.Fatal(err)
		}
		return est
	}
	step4 := ask(observedRequest(0, true))
	src.mu.Lock()
	src.history = append(src.history, 0.003) // a run seals before step 5
	src.mu.Unlock()
	step5 := ask(observedRequest(0.30, false))
	if len(step4.Presets) == 0 || step4.Presets[0].ID != PresetQuick {
		t.Fatalf("presets %+v", step4.Presets)
	}
	quick := step4.Presets[0].EstCostUSD
	if !step5.EstCostUSD.Known || quick != step4.EstCostUSD || quick.Low != step5.EstCostUSD.Low || quick.High != step5.EstCostUSD.High {
		t.Fatalf("quick look %+v, step 4 %+v, step 5 %+v: want one range", quick, step4.EstCostUSD, step5.EstCostUSD)
	}
	if quick.Low != 0.04 || quick.High != 0.1 {
		t.Fatalf("range %+v, want 10 runs of 0.004 to 0.01", quick)
	}
	// The default budget follows the high end by SuggestBudget: 3 times, at least $0.25, rounded up.
	if step5.Budget.SuggestedUSD != SuggestBudget(step5.EstCostUSD.High) || step5.Budget.SuggestedUSD != 0.30 {
		t.Fatalf("suggested %v", step5.Budget.SuggestedUSD)
	}
}
