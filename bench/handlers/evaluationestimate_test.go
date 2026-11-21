package handlers

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

type pricedForecast struct {
	fakeForecast
	priced map[string]bool
}

func (p *pricedForecast) Priced(m string) bool { return p.priced[m] }

func estimateSuites() *fakeSuites {
	return &fakeSuites{ids: []string{"coding-core", "plain"}, suites: map[string]*schemas.Suite{
		"coding-core": {ID: "coding-core", Version: "1.0.0", TaskIDs: []string{"t3", "t1", "t2"},
			Tasks: []*schemas.Task{{ID: "t3"}, {ID: "t1"}, {ID: "t2"}},
			Probes: []*schemas.ProbeSpec{
				{ID: "model-outage", PS2Family: schemas.FamilyUnavailableModel, Layer: schemas.LayerGateway, Apply: schemas.ProbeApply{Fault: &schemas.FaultPlan{Mode: "persistent"}}},
				{ID: "stale-notes", PS2Family: schemas.FamilyMisleadingInstruction, Layer: schemas.LayerFixture},
			}},
		"plain": {ID: "plain", Version: "2", TaskIDs: []string{"a"}, Tasks: []*schemas.Task{{ID: "a"}}},
	}}
}

func TestEstimateEvaluationKnown(t *testing.T) {
	src := &pricedForecast{priced: map[string]bool{"gpt-x": true}}
	req := &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"coding-core", "plain", "missing"},
		TasksPerBenchmark: 2, Faults: []string{"clean", "model-down", "misleading-instruction", "broken-tool"}}
	est, err := EstimateEvaluation(context.Background(), estimateSuites(), src, req)
	if err != nil {
		t.Fatal(err)
	}
	cc := est.PerBenchmark[0]
	if !slices.Equal(cc.Conditions, []string{"clean", "model-outage", "stale-notes"}) || len(cc.FaultsSkip) != 1 || cc.FaultsSkip[0].Fault != "broken-tool" {
		t.Fatalf("coding-core conditions %v skip %v", cc.Conditions, cc.FaultsSkip)
	}
	if cc.Runs != 1*2*3*2 || cc.Tasks != 2 {
		t.Fatalf("coding-core runs %d tasks %d", cc.Runs, cc.Tasks)
	}
	pl := est.PerBenchmark[1]
	if !slices.Equal(pl.Conditions, []string{"clean", schemas.GenericProbeModelDown}) || pl.Runs != 1*1*2*2 {
		t.Fatalf("plain %v runs %d", pl.Conditions, pl.Runs)
	}
	if est.PerBenchmark[2].Skipped == "" {
		t.Fatal("missing benchmark must be skipped")
	}
	if est.Runs != 12+4 || !est.EstCostUSD.Known || est.EstCostUSD.Low != 2 || est.EstCostUSD.High != 6 {
		t.Fatalf("totals %+v", est)
	}
	// The experiment the forecast saw: the one arm only (no hidden repeat).
	exp := src.expFor("plain@2")
	if exp == nil || exp.Suite != "plain@2" || len(exp.Arms) != 1 || exp.Arms["A"].Set["model"] != "gpt-x" || exp.Base != "agents/codex.yaml" {
		t.Fatalf("experiment %+v", exp)
	}
}

func TestEstimateEvaluationUnpricedNeverGuesses(t *testing.T) {
	src := &pricedForecast{priced: map[string]bool{}}
	req := &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}, {Agent: "codex", Model: "glm-9"}}, Benchmarks: []string{"plain"}}
	est, err := EstimateEvaluation(context.Background(), estimateSuites(), src, req)
	if err != nil {
		t.Fatal(err)
	}
	if est.EstCostUSD.Known || est.PerBenchmark[0].EstCostUSD.Known {
		t.Fatal("unpriced model must leave the cost unknown")
	}
	if len(est.UnpricedModels) != 2 || est.EstCostUSD.Reason != "no price for gpt-x, glm-9" || est.PerBenchmark[0].EstCostUSD.Reason == "" {
		t.Fatalf("unpriced %v reason %q", est.UnpricedModels, est.EstCostUSD.Reason)
	}
	if est.Runs != 2*1*1*EvalDefaultTrials {
		t.Fatalf("runs %d", est.Runs)
	}
	// No pricer at all: unknown too. Plan refused: unknown, with a note.
	plain := &fakeForecast{estimateErr: errors.Join(ErrPlanRefused, errors.New("no lock"))}
	est, err = EstimateEvaluation(context.Background(), estimateSuites(), plain, &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"plain"}})
	if err != nil || est.EstCostUSD.Known || len(est.PerBenchmark[0].Notes) == 0 {
		t.Fatalf("refused %+v %v", est, err)
	}
	// Nil forecast: runs only.
	est, err = EstimateEvaluation(context.Background(), estimateSuites(), nil, &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Pack: "all-compatible"})
	if err != nil || est.EstCostUSD.Known || len(est.PerBenchmark) != 2 || est.Runs != 3*2+1*2 {
		t.Fatalf("nil forecast %+v %v", est, err)
	}
}

func TestEstimateEvaluationBadRequest(t *testing.T) {
	for _, req := range []*EvalEstimateRequest{
		{Benchmarks: []string{"plain"}},
		{Arms: []EvalEstimateArm{{Agent: "codex", Model: "m"}}},
		{Arms: []EvalEstimateArm{{Agent: "codex", Model: "m"}}, Benchmarks: []string{"plain"}, Trials: 99},
		{Arms: []EvalEstimateArm{{Agent: "../x", Model: "m"}}, Benchmarks: []string{"plain"}},
		{Arms: []EvalEstimateArm{{Agent: "codex", Model: "m"}}, Pack: "nope"},
	} {
		if _, err := EstimateEvaluation(context.Background(), estimateSuites(), nil, req); !errors.Is(err, errQuickBad) {
			t.Fatalf("%+v: got %v, want bad request", req, err)
		}
	}
}

// TestEstimateForecastsTheTasksTheLaunchRuns: the estimate and the launch
// share one picker, spread over the admitted tasks, admission read from the
// lock alone.
func TestEstimateForecastsTheTasksTheLaunchRuns(t *testing.T) {
	admitted := func() schemas.TaskLock {
		return schemas.TaskLock{Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{Admitted: true}}
	}
	st := &schemas.Suite{ID: "wide", Version: "1", TaskIDs: []string{"t1", "t2", "t3", "t4", "t5"},
		Tasks: []*schemas.Task{{ID: "t1"}, {ID: "t2"}, {ID: "t3"}, {ID: "t4"}, {ID: "t5"}},
		Lock:  &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{"t1": admitted(), "t2": admitted(), "t3": {Status: schemas.TaskAdmitted}, "t4": admitted(), "t5": admitted()}}}
	want, _ := evalPickTasks(st, schemas.SplitPublic, 2, nil)
	if !slices.Equal(want, []string{"t1", "t4"}) {
		t.Fatalf("pick = %v, want spread over admitted t1 t2 t4 t5", want)
	}
	// A suite head (no Tasks) picks the same.
	head := *st
	head.Tasks = nil
	if got, _ := evalPickTasks(&head, schemas.SplitPublic, 2, nil); !slices.Equal(got, want) {
		t.Fatalf("head pick = %v, want %v", got, want)
	}
	src := &fakeForecast{}
	suites := &fakeSuites{ids: []string{"wide"}, suites: map[string]*schemas.Suite{"wide": st}}
	if _, err := EstimateEvaluation(context.Background(), suites, src, &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"wide"}, TasksPerBenchmark: 2}); err != nil {
		t.Fatal(err)
	}
	exp := src.expFor("wide@1")
	if exp == nil || len(exp.Conditions) == 0 || !slices.Equal(exp.Conditions[0].Tasks.IDs, want) {
		t.Fatalf("forecast tasks = %+v, want %v", exp, want)
	}
}

// TestEstimateForecastCache: the same estimate twice asks the forecast once;
// another agent config or another model asks again; errors are not cached.
func TestEstimateForecastCache(t *testing.T) {
	src := &fakeForecast{}
	cache := newForecastCache()
	req := func(model string) *EvalEstimateRequest {
		return &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: model}}, Benchmarks: []string{"coding-core", "plain"}}
	}
	agents := map[string]*schemas.AgentConfig{"agents/codex.yaml": {ID: "codex", Adapter: "command"}}
	for i := 0; i < 3; i++ {
		if _, err := estimateEvaluation(context.Background(), estimateSuites(), src, req("gpt-x"), agents, "/org", cache); err != nil {
			t.Fatal(err)
		}
	}
	if n := src.calls(); n != 2 {
		t.Fatalf("forecast calls = %d, want 2 (one per benchmark, then cached)", n)
	}
	_, _ = estimateEvaluation(context.Background(), estimateSuites(), src, req("gpt-y"), agents, "/org", cache)
	if n := src.calls(); n != 4 {
		t.Fatalf("calls after a new model = %d, want 4", n)
	}
	agents["agents/codex.yaml"] = &schemas.AgentConfig{ID: "codex", Adapter: "command", Model: "changed"}
	_, _ = estimateEvaluation(context.Background(), estimateSuites(), src, req("gpt-x"), agents, "/org", cache)
	if n := src.calls(); n != 6 {
		t.Fatalf("calls after an agent config change = %d, want 6", n)
	}
	// Expired entries are asked again.
	cache.now = func() time.Time { return time.Now().Add(forecastCacheTTL + time.Second) }
	_, _ = estimateEvaluation(context.Background(), estimateSuites(), src, req("gpt-x"), agents, "/org", cache)
	if n := src.calls(); n != 8 {
		t.Fatalf("calls after expiry = %d, want 8", n)
	}
	bad := &fakeForecast{estimateErr: errors.Join(ErrPlanRefused, errors.New("no lock"))}
	errCache := newForecastCache()
	for i := 0; i < 2; i++ {
		_, _ = estimateEvaluation(context.Background(), estimateSuites(), bad, req("gpt-x"), agents, "/org", errCache)
	}
	if n := bad.calls(); n != 4 {
		t.Fatalf("refused forecast calls = %d, want 4 (errors never cached)", n)
	}
}

func TestEstimateEvaluationPower(t *testing.T) {
	req := &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"coding-core", "plain", "missing"},
		TasksPerBenchmark: 3, Trials: 4, Faults: []string{"clean"}}
	est, err := EstimateEvaluation(context.Background(), estimateSuites(), nil, req)
	if err != nil {
		t.Fatal(err)
	}
	p := est.Power
	if p.SDSource != "assumed" || len(p.PerBenchmark) != 2 || p.Overall.Tasks != 4 {
		t.Fatalf("power %+v", p)
	}
	// 3 and 1 tasks cannot reach alpha 0.025 with a sign-flip test.
	for _, b := range p.PerBenchmark {
		if b.Decidable || b.MDEpp != nil {
			t.Fatalf("benchmark %+v should not be decidable", b)
		}
	}
	if p.Overall.MDEpp == nil || *p.Overall.MDEpp <= 10 {
		t.Fatalf("overall mde %v", p.Overall.MDEpp)
	}
	if !strings.Contains(p.Advice, "point change overall") || !strings.Contains(p.Advice, "trials") {
		t.Fatalf("advice %q", p.Advice)
	}
}

func TestEvalPowerDecidable(t *testing.T) {
	p := evalPower([]EvalBenchmarkEstimate{{Benchmark: "x", Tasks: 100}}, 2)
	b := p.PerBenchmark[0]
	if !b.Decidable || b.MDEpp == nil || *b.MDEpp > 10 || p.Overall.MDEpp == nil {
		t.Fatalf("power %+v", p)
	}
	if !strings.Contains(p.Advice, "Enough tasks") || strings.Contains(p.Advice, "trials") {
		t.Fatalf("advice %q", p.Advice)
	}
}
