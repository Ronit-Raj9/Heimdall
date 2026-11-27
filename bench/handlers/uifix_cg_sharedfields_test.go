package handlers

import "testing"

func TestCGSharedFieldsHidden(t *testing.T) {
	for _, part := range []string{"report", "report_card"} {
		v := map[string]any{"totals": map[string]any{"A": map[string]any{"cost_usd": 1.0, "fault_cost_usd": 0.5, "fault_cost_runs": 2.0}}}
		out := withoutNewSharedFields(part, v).(map[string]any)
		a := out["totals"].(map[string]any)["A"].(map[string]any)
		if _, ok := a["fault_cost_usd"]; ok || a["cost_usd"] != 1.0 {
			t.Fatalf("%s: %v", part, a)
		}
	}
}
