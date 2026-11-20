package engine

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestAccountingMetricsAddCostUnderSpendCap guards 3-C-05: a display metric
// subset without cost_usd must not hide the measured cost from the spend cap.
func TestAccountingMetricsAddCostUnderSpendCap(t *testing.T) {
	reg := &schemas.Registry{Metrics: []schemas.Metric{
		fakeMetric{part: part{name: "verified_success"}},
		fakeMetric{part: part{name: costMetric}},
		fakeMetric{part: part{name: simulatorCostMetric}},
	}}
	e := &schemas.Experiment{Metrics: []string{"verified_success"}}
	sel := selectMetrics(reg, e.Metrics)
	if got := accountingMetrics(reg, e, sel); len(got) != 0 {
		t.Fatalf("no budget: got %d extra metrics, want 0", len(got))
	}
	e.Budget.TotalUSD = 5
	got := accountingMetrics(reg, e, sel)
	if len(got) != 2 || metricName(got[0]) != costMetric || metricName(got[1]) != simulatorCostMetric {
		t.Fatalf("budget: got %v, want cost_usd and simulator_cost_usd", got)
	}
	e.Metrics = []string{"verified_success", costMetric}
	if got := accountingMetrics(reg, e, selectMetrics(reg, e.Metrics)); len(got) != 1 || metricName(got[0]) != simulatorCostMetric {
		t.Fatalf("cost already selected: got %v", got)
	}
}
