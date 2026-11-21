package handlers

import (
	"context"
	"errors"
	"slices"
	"testing"

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
	// The experiment the forecast saw: the one arm only (no hidden repeat), first N sorted admitted tasks.
	exp := src.lastExp
	if exp.Suite != "plain@2" || len(exp.Arms) != 1 || exp.Arms["A"].Set["model"] != "gpt-x" || exp.Base != "agents/codex.yaml" {
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
