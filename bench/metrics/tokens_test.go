package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestTokens(t *testing.T) {
	gw := schemas.EvidenceGateway
	withTokens := func(id string, tk *schemas.Tokens) schemas.GatewayRequest {
		r := okMsg(id)
		r.Tokens = tk
		return r
	}
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{
			name: "four buckets summed over requests",
			ev:   &schemas.Evidence{Requests: completeLog(withTokens("a", tok(100, 2000, 300, 40)), withTokens("b", tok(10, 2300, 0, 60)))},
			want: schemas.Observed(4810, gw),
		},
		{
			name: "non-message rows ignored",
			ev: &schemas.Evidence{Requests: completeLog(
				withTokens("a", tok(1, 2, 3, 4)),
				schemas.GatewayRequest{ID: "ct", Kind: "count_tokens", Tokens: tok(500, 0, 0, 0)},
				schemas.GatewayRequest{ID: "m", Kind: "models"},
			)},
			want: schemas.Observed(10, gw),
		},
		{name: "no requests", ev: &schemas.Evidence{Requests: completeLog()}, want: schemas.Observed(0, gw)},
		{name: "one row without usage", ev: &schemas.Evidence{Requests: completeLog(okMsg("a"), withTokens("b", nil))}, want: schemas.Unknown("message request b has no usage", gw)},
		{
			name: "incomplete log",
			ev:   &schemas.Evidence{Requests: &schemas.RequestLog{Requests: []schemas.GatewayRequest{okMsg("a")}}},
			want: schemas.Unknown(reasonIncomplete, gw),
		},
		{name: "no log", ev: &schemas.Evidence{}, want: schemas.Unknown(reasonNoLog, gw)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoLog, gw)},
	}
	m := newTokensMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}
