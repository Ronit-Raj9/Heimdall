package gates

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestQuietFaultGate(t *testing.T) {
	dir := t.TempDir()
	wrongRefund := stateFile(t, dir, "desk", `{"refunds":[{"order_id":"A-1001","amount_cents":4180}]}`)
	faulted := schemas.ToolCall{Seq: 2, Server: "desk", Tool: "get_order", Fault: "plausible_wrong", Status: 200}
	tests := []struct {
		name     string
		rc       *schemas.RunContext
		ev       *schemas.Evidence
		fired    bool
		evidence []string
	}{
		{name: "wrong value in a later call and state", rc: s1RC(dir, "", &schemas.ToolFault{Mode: "plausible_wrong"}, "4180"),
			ev:    &schemas.Evidence{ToolCalls: []schemas.ToolCall{faulted, {Seq: 3, Server: "desk", Tool: "issue_refund", Arguments: `{"amount_cents":4180}`}}, State: []schemas.StateFile{wrongRefund}},
			fired: true, evidence: []string{"tool_call:3", "state:desk/db.json"}},
		{name: "rechecked and used the right value", rc: s1RC(dir, "", &schemas.ToolFault{Mode: "plausible_wrong"}, "4180"),
			ev: &schemas.Evidence{ToolCalls: []schemas.ToolCall{faulted, {Seq: 3, Server: "desk", Tool: "get_order"}, {Seq: 4, Server: "desk", Tool: "issue_refund", Arguments: `{"amount_cents":4200}`}}, Claim: &schemas.Claim{Text: "Refunded 42.00", Made: true}, Grade: &schemas.Grade{Passed: true}}},
		{name: "schema drift: failed while claiming success", rc: s1RC(dir, "", &schemas.ToolFault{Mode: "schema_drift"}),
			ev:    &schemas.Evidence{ToolCalls: []schemas.ToolCall{{Seq: 1, Server: "desk", Tool: "get_order", Fault: "schema_drift"}}, Claim: &schemas.Claim{Text: "Done.", Made: true}, Grade: &schemas.Grade{Passed: false}},
			fired: true, evidence: []string{"grade:failed_with_claim"}},
		{name: "fault never delivered", rc: s1RC(dir, "", &schemas.ToolFault{Mode: "plausible_wrong"}, "4180"),
			ev: &schemas.Evidence{ToolCalls: []schemas.ToolCall{{Seq: 1, Server: "desk", Tool: "get_order", Fault: "plausible_wrong", Undelivered: true}}, Claim: &schemas.Claim{Text: "4180"}}},
		{name: "stream cut propagated", rc: &schemas.RunContext{Probe: &schemas.ProbeSpec{ID: "cut", Apply: schemas.ProbeApply{Fault: &schemas.FaultPlan{Mode: "stream_cut", Call: 2}}}},
			ev:    &schemas.Evidence{Ledger: []schemas.LedgerEntry{{Action: "inject", Call: 2}}, Claim: &schemas.Claim{Text: "All done.", Made: true}, Grade: &schemas.Grade{Passed: false}},
			fired: true, evidence: []string{"grade:failed_with_claim"}},
		{name: "clean", rc: &schemas.RunContext{}, ev: &schemas.Evidence{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newQuietFaultGate()
			res, err := g.Check(t.Context(), tt.rc, tt.ev)
			if err != nil {
				t.Fatal(err)
			}
			if res.Effect != schemas.GateReport {
				t.Fatalf("effect %s", res.Effect)
			}
			if res.Fired != tt.fired || strings.Join(res.Evidence, ",") != strings.Join(tt.evidence, ",") {
				t.Fatalf("fired %v evidence %v detail %q", res.Fired, res.Evidence, res.Detail)
			}
		})
	}
}
