package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestImproveEstimateWire(t *testing.T) {
	if handlers.ImproveEstimateInputs == nil {
		t.Fatal("the estimate hook is not set")
	}
	s := &schemas.Suite{}
	for i := range 12 {
		s.Tasks = append(s.Tasks, &schemas.Task{ID: string(rune('a' + i))})
	}
	s.Tasks = append(s.Tasks, &schemas.Task{ID: "trap", Impossible: true}, &schemas.Task{ID: "other", Impossible: true})
	spec := &improve.Spec{Name: "w", Agent: "agents/a.yaml@v2", Suite: "x", Splits: improve.SplitSpec{Honeypots: []string{"trap"}}}
	spec.Defaults()
	in, err := improveEstimatePool(s, spec)
	if err != nil {
		t.Fatal(err)
	}
	// 12 tasks: rounding gives 5, 4 and 4, one too many, so dev shrinks to 4; the honeypot is counted, the other impossible task left out.
	if in.Pool != 12 || in.Splits != (improve.SplitCounts{Practice: 4, Gate: 4, HeldOut: 4, Impossible: 1}) {
		t.Fatalf("pool %d splits %+v", in.Pool, in.Splits)
	}
	if p, v := improveEstimateAgent(spec.Agent); p != "agents/a.yaml" || v != 2 {
		t.Fatalf("agent %q %d", p, v)
	}
	est := &handlers.EvalEstimate{Runs: 5, EstCostUSD: handlers.EvalCostRange{Low: 0.1, High: 0.3, Known: true}, EstMinutes: handlers.EvalMinutesRange{Low: 5, High: 15, Known: true}}
	improveEstimateCost(&in, est)
	if in.PerRunUSD < 0.0399 || in.PerRunUSD > 0.0401 || in.RunSource != "forecast" || in.PerRunMin != 2 {
		t.Fatalf("cost %+v", in)
	}
}
