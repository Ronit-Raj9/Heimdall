package handlers

// Fields lane CG added to answers a shared page reads, kept off the shared
// payload until the owner decides (same rule as sharedfields_srv.go):
//
//	report, report_card  totals.<arm>.fault_cost_usd, fault_cost_runs
var cgSharedTotalsHidden = []string{"fault_cost_usd", "fault_cost_runs"}

func withoutCGSharedFields(part string, v any) any {
	if part != "report" && part != "report_card" {
		return v
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if totals, ok := m["totals"].(map[string]any); ok {
		for _, t := range totals {
			if tm, ok := t.(map[string]any); ok {
				for _, k := range cgSharedTotalsHidden {
					delete(tm, k)
				}
			}
		}
	}
	return m
}
