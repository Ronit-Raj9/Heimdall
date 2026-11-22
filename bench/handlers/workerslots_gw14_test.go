package handlers

import (
	"context"
	"errors"
	"testing"
)

// TestEvalSlotsBoundByWorkerSlots (N90): the estimate's runs at once are
// bounded by the worker's slots after the org quota, and the answer says
// how many the worker runs; a failed lookup does not bound them.
func TestEvalSlotsBoundByWorkerSlots(t *testing.T) {
	src := &pricedForecast{priced: map[string]bool{"gpt-x": true}}
	r := EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"coding-core", "plain"}, Concurrency: 8}
	r.orgSlots = 4
	r.workerSlots = 2
	est, err := EstimateEvaluation(context.Background(), estimateSuites(), src, &r)
	if err != nil {
		t.Fatal(err)
	}
	if est.Slots.WorkerSlots != 2 || est.Slots.Concurrency != 2 || est.Slots.Parallel != 2 || est.Slots.OrgSlots != 4 {
		t.Fatalf("slots %+v, want concurrency 2, parallel 2, worker 2, org 4", est.Slots)
	}
	var gotTarget string
	s := Services{WorkerSlots: func(_ context.Context, target string) (int, error) { gotTarget = target; return 3, nil }}
	if n := workerSlotsOf(context.Background(), s, "worker:gpu"); n != 3 || gotTarget != "worker:gpu" {
		t.Fatalf("worker slots %d for %q", n, gotTarget)
	}
	s.WorkerSlots = func(context.Context, string) (int, error) { return 5, errors.New("db down") }
	if n := workerSlotsOf(context.Background(), s, ""); n != 0 {
		t.Fatalf("failed lookup bounded the estimate: %d", n)
	}
	if n := workerSlotsOf(context.Background(), Services{}, ""); n != 0 {
		t.Fatalf("local mode bounded the estimate: %d", n)
	}
}
