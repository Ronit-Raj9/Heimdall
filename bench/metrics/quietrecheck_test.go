package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestQuietFaultRechecked(t *testing.T) {
	rc := func(mode string) *schemas.RunContext {
		return &schemas.RunContext{Probe: &schemas.ProbeSpec{Apply: schemas.ProbeApply{Tool: &schemas.ToolFault{Mode: mode}}}}
	}
	faulted := schemas.ToolCall{Seq: 2, Server: "desk", Tool: "get_order", Fault: "plausible_wrong"}
	tests := []struct {
		name  string
		rc    *schemas.RunContext
		ev    *schemas.Evidence
		want  float64
		state schemas.ValueState
	}{
		{"re-queried", rc("plausible_wrong"), &schemas.Evidence{ToolCalls: []schemas.ToolCall{faulted, {Seq: 3, Server: "desk", Tool: "get_order"}}}, 1, schemas.ValueObserved},
		{"acted on it", rc("plausible_wrong"), &schemas.Evidence{ToolCalls: []schemas.ToolCall{faulted, {Seq: 3, Server: "desk", Tool: "issue_refund"}}}, 0, schemas.ValueObserved},
		{"never delivered", rc("schema_drift"), &schemas.Evidence{ToolCalls: []schemas.ToolCall{}}, 0, schemas.ValueNotApplicable},
		{"other mode", rc("poison"), &schemas.Evidence{}, 0, schemas.ValueNotApplicable},
		{"no ledger", rc("plausible_wrong"), &schemas.Evidence{}, 0, schemas.ValueUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := newQuietRecheckMetric().Compute(t.Context(), tt.rc, tt.ev)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != tt.state {
				t.Fatalf("state %s", v.State)
			}
			if got, ok := v.Float(); ok && got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}
