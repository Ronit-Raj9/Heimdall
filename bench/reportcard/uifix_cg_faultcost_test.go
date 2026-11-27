package reportcard

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestTotalsFaultCost: the cost of fault runs is summed apart from the
// clean cost; attacks and clean runs stay out; an unknown cost gives none.
func TestTotalsFaultCost(t *testing.T) {
	cost := func(v float64) map[string]schemas.Value {
		return map[string]schemas.Value{metricCostUSD: {State: schemas.ValueObserved, V: &v}}
	}
	cr := childRuns{arms: map[string][]graded{"A": {
		{task: "t1", condition: schemas.ConditionClean, success: 1, metrics: cost(0.5)},
		{task: "t1", condition: "model-outage", success: 0, metrics: cost(0.25)},
		{task: "t2", condition: "model-outage", success: 1, metrics: cost(0.25)},
		{task: "t1", condition: "inject", success: 1, metrics: cost(9)},
	}}}
	faultOf := func(_ childRuns, c string) string {
		switch c {
		case "model-outage":
			return "model-down"
		case "inject":
			return Attack
		}
		return ""
	}
	tot := totals([]childRuns{cr}, []string{"A"}, faultOf)["A"]
	if tot.CostUSD == nil || *tot.CostUSD != 0.5 || tot.FaultCostUSD == nil || *tot.FaultCostUSD != 0.5 || tot.FaultCostRuns != 2 {
		t.Fatalf("%+v", tot)
	}
	cr.arms["A"][1].metrics = nil
	tot = totals([]childRuns{cr}, []string{"A"}, faultOf)["A"]
	if tot.FaultCostUSD != nil || tot.FaultCostRuns != 1 {
		t.Fatalf("unknown cost: %+v", tot)
	}
}
