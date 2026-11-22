package reportcard

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBifrostRows(t *testing.T) {
	obs := func(v float64) schemas.Value { return schemas.Value{State: schemas.ValueObserved, V: &v} }
	cr := childRuns{arms: map[string][]graded{
		"A": {
			{metrics: map[string]schemas.Value{metricFallbackUsed: obs(1), metricSemanticCacheHits: obs(2), metricCostUSD: obs(0.5), metricInputTokens: obs(100)}},
			{metrics: map[string]schemas.Value{metricFallbackUsed: obs(0), metricSemanticCacheHits: obs(1), metricModelRetries: obs(3)}},
		},
		"B": {{metrics: map[string]schemas.Value{}}},
	}}
	rows := bifrostRows([]childRuns{cr}, []string{"A", "B", "C"})
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	a := rows[0]
	if a.Runs != 2 || a.FallbackKnown != 2 || *a.FallbackRuns != 1 || *a.CacheHits != 3 || *a.Retries != 3 || *a.InputTokens != 100 || *a.CostUSD != 0.5 || a.CostRuns != 1 {
		t.Errorf("A = %+v", a)
	}
	b := rows[1]
	if b.FallbackRuns != nil || b.CacheHits != nil || b.CostUSD != nil || b.Runs != 1 {
		t.Errorf("B = %+v", b)
	}
}
