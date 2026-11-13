package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSteps(t *testing.T) {
	gw := schemas.EvidenceGateway
	retried := okMsg("b")
	retried.Attempts = attempts(3)
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "three turns", ev: &schemas.Evidence{Requests: completeLog(okMsg("a"), okMsg("b"), okMsg("c"))}, want: schemas.Observed(3, gw)},
		{name: "bifrost retries do not add steps", ev: &schemas.Evidence{Requests: completeLog(okMsg("a"), retried)}, want: schemas.Observed(2, gw)},
		{
			name: "fallback row does not add a step",
			ev: &schemas.Evidence{Requests: completeLog(
				schemas.GatewayRequest{ID: "a", Kind: kindMessage, Status: "error", Attempts: attempts(2)},
				schemas.GatewayRequest{ID: "a-fb", ParentID: "a", FallbackIndex: 1, Kind: kindMessage, Status: statusSuccess},
			)},
			want: schemas.Observed(1, gw),
		},
		{
			name: "sdk retry after a failed request does not add a step",
			ev: &schemas.Evidence{Requests: completeLog(
				okMsg("a"),
				schemas.GatewayRequest{ID: "b1", Kind: kindMessage, Status: "error", ErrorStatus: 503, Attempts: attempts(3)},
				schemas.GatewayRequest{ID: "b2", Kind: kindMessage, Status: "error", ErrorStatus: 503, Attempts: attempts(3)},
				okMsg("b3"),
			)},
			want: schemas.Observed(2, gw),
		},
		{
			name: "parallel tool calls are one step",
			ev: &schemas.Evidence{Requests: completeLog(schemas.GatewayRequest{
				ID: "a", Kind: kindMessage, Status: statusSuccess, ToolCalls: calls("t1", "t2", "t3"),
			})},
			want: schemas.Observed(1, gw),
		},
		{
			name: "non-message kinds ignored",
			ev: &schemas.Evidence{Requests: completeLog(
				schemas.GatewayRequest{ID: "ct", Kind: "count_tokens", Status: statusSuccess},
				schemas.GatewayRequest{ID: "m", Kind: "models", Status: statusSuccess},
				okMsg("a"),
			)},
			want: schemas.Observed(1, gw),
		},
		{name: "no requests is zero turns", ev: &schemas.Evidence{Requests: completeLog()}, want: schemas.Observed(0, gw)},
		{name: "no log", ev: &schemas.Evidence{}, want: schemas.Unknown(reasonNoLog, gw)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoLog, gw)},
		{
			name: "incomplete log",
			ev:   &schemas.Evidence{Requests: &schemas.RequestLog{Requests: []schemas.GatewayRequest{okMsg("a")}, Reason: "timeout"}},
			want: schemas.Unknown("timeout", gw),
		},
		{
			name: "processing row",
			ev:   &schemas.Evidence{Requests: completeLog(okMsg("a"), schemas.GatewayRequest{ID: "b", Kind: kindMessage, Status: statusProcessing})},
			want: schemas.Unknown("still processing", gw),
		},
	}
	m := newStepsMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}
