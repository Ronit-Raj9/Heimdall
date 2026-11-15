package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestToolCallsExecuted(t *testing.T) {
	m := newToolCallsExecutedMetric()
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.ValueState
		v    float64
	}{
		{name: "shim evidence", ev: &schemas.Evidence{RequestedTools: []schemas.ToolCallRecord{{Requested: true}}, ShimLog: []schemas.ShimCall{{Tool: "bash", Call: 1, Action: "pass"}, {Tool: "bash", Call: 2, Action: "crash"}}}, want: schemas.ValueObserved, v: 2},
		{name: "native fallback", ev: &schemas.Evidence{Events: []schemas.Event{{Type: schemas.EventToolResult, ToolCallID: "t1"}}}, want: schemas.ValueObserved, v: 1},
		{name: "missing evidence", ev: &schemas.Evidence{RequestedTools: []schemas.ToolCallRecord{{Requested: true}}}, want: schemas.ValueUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.Compute(nil, nil, tt.ev)
			if err != nil || got.State != tt.want {
				t.Fatalf("got %+v, err %v", got, err)
			}
			if tt.want == schemas.ValueObserved {
				v, ok := got.Float()
				if !ok || v != tt.v {
					t.Fatalf("value = %v, want %v", v, tt.v)
				}
			}
		})
	}
}
