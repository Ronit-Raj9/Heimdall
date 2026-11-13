package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRetryAmplification(t *testing.T) {
	gw := schemas.EvidenceGateway
	withAttempts := func(id string, n int) schemas.GatewayRequest {
		r := okMsg(id)
		r.Attempts = attempts(n)
		return r
	}
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "no retries", ev: &schemas.Evidence{Requests: completeLog(okMsg("a"), okMsg("b"))}, want: schemas.Observed(1, gw)},
		{name: "retries inside rows", ev: &schemas.Evidence{Requests: completeLog(withAttempts("a", 3), withAttempts("b", 1))}, want: schemas.Observed(2, gw)},
		{name: "row without attempt trail counts once", ev: &schemas.Evidence{Requests: completeLog(withAttempts("a", 0), withAttempts("b", 2))}, want: schemas.Observed(1.5, gw)},
		{
			name: "fallback attempts join their parent",
			ev: &schemas.Evidence{Requests: completeLog(
				schemas.GatewayRequest{ID: "a", Kind: kindMessage, Status: "error", Attempts: attempts(3)},
				schemas.GatewayRequest{ID: "a-fb", ParentID: "a", FallbackIndex: 1, Kind: kindMessage, Status: statusSuccess, Attempts: attempts(1)},
			)},
			want: schemas.Observed(4, gw),
		},
		{
			name: "failed requests stay in the denominator",
			ev: &schemas.Evidence{Requests: completeLog(
				okMsg("a"),
				schemas.GatewayRequest{ID: "b", Kind: kindMessage, Status: "error", Attempts: attempts(3)},
			)},
			want: schemas.Observed(2, gw),
		},
		{
			name: "non-message rows ignored",
			ev:   &schemas.Evidence{Requests: completeLog(okMsg("a"), schemas.GatewayRequest{ID: "ct", Kind: "count_tokens", Attempts: attempts(5)})},
			want: schemas.Observed(1, gw),
		},
		{name: "no message requests", ev: &schemas.Evidence{Requests: completeLog()}, want: schemas.NotApplicable()},
		{name: "incomplete log", ev: &schemas.Evidence{Requests: &schemas.RequestLog{Reason: "rows missing"}}, want: schemas.Unknown("rows missing", gw)},
		{name: "no log", ev: &schemas.Evidence{}, want: schemas.Unknown(reasonNoLog, gw)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoLog, gw)},
	}
	m := newRetryAmplificationMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}
