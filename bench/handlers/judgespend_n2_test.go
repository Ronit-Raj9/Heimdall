package handlers

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestSpentCountsJudge: "Spent so far" (status spent_usd, which the
// evaluation children and Home read) counts the judge's spend, the way the
// scheduler counts it against the cap (N2, PQ1-11: $0.0027 shown while the
// judge cost $0.03).
func TestSpentCountsJudge(t *testing.T) {
	obs := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }
	runs := []RunView{
		{Run: schemas.Run{RunID: "r1"}, Sealed: true},
		{Run: schemas.Run{RunID: "r2"}, Sealed: true},
		{Run: schemas.Run{RunID: "r3"}, Sealed: true},
	}
	metrics := map[string]runMetricsRead{
		"r1": {found: true, values: map[string]schemas.Value{costMetric: obs(0.0017), judgeCostMetric: obs(0.02)}},
		"r2": {found: true, values: map[string]schemas.Value{costMetric: obs(0.001), judgeCostMetric: obs(0.01)}},
		"r3": {found: true, values: map[string]schemas.Value{costMetric: obs(0.0005), judgeCostMetric: schemas.NotApplicable()}},
	}
	got, ok := spentUSD(runs, costsOf(runs, metrics)).Float()
	if !ok || math.Abs(got-0.0332) > 1e-9 {
		t.Fatalf("spent %v %v, want 0.0332 (agent 0.0032 plus judge 0.03)", got, ok)
	}
}
