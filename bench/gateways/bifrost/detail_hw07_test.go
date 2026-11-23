package bifrost

import "testing"

// TestParseDetailFaultPlanID checks that the heimdall plugin's plan id, which
// the Bifrost logging plugin stores in the log row metadata, lands on the
// exported request row (L4: the fault_delivery gate reads it at seal).
func TestParseDetailFaultPlanID(t *testing.T) {
	row := `{"id":"l1","object":"chat.completion","provider":"openai","model":"gpt-6-luna","status":"success",
		"metadata":{"heimdall_run_id":"r_abc","heimdall_fault_plan_id":"r_abc:persistent","heimdall_fault":"rate_limit"}}`
	p, err := parseDetail([]byte(row))
	if err != nil {
		t.Fatal(err)
	}
	if p.Req.FaultPlanID != "r_abc:persistent" {
		t.Fatalf("FaultPlanID = %q", p.Req.FaultPlanID)
	}
	p, err = parseDetail([]byte(`{"id":"l2","object":"chat.completion","model":"gpt-6-luna","status":"success"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Req.FaultPlanID != "" {
		t.Fatalf("row without metadata got FaultPlanID %q", p.Req.FaultPlanID)
	}
}
