package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestToolCalls(t *testing.T) {
	gw, native := schemas.EvidenceGateway, schemas.EvidenceNative
	withCalls := func(id string, ids ...string) schemas.GatewayRequest {
		r := okMsg(id)
		r.ToolCalls = calls(ids...)
		return r
	}
	streamCall := func(id string) schemas.Event {
		return schemas.Event{Type: schemas.EventToolCall, Source: schemas.SourceAgentStream, ToolCallID: id}
	}
	incomplete := &schemas.RequestLog{Requests: []schemas.GatewayRequest{withCalls("a", "t1")}, Reason: "rows missing"}
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "parallel calls in one turn count individually", ev: &schemas.Evidence{Requests: completeLog(withCalls("a", "t1", "t2", "t3"))}, want: schemas.Observed(3, gw)},
		{name: "across turns", ev: &schemas.Evidence{Requests: completeLog(withCalls("a", "t1"), withCalls("b", "t2", "t3"), okMsg("c"))}, want: schemas.Observed(3, gw)},
		{
			name: "same id on fallback and parent counts once",
			ev: &schemas.Evidence{Requests: completeLog(
				withCalls("a", "t1"),
				schemas.GatewayRequest{ID: "a-fb", ParentID: "a", Kind: kindMessage, Status: statusSuccess, ToolCalls: calls("t1")},
			)},
			want: schemas.Observed(1, gw),
		},
		{name: "calls without ids each count", ev: &schemas.Evidence{Requests: completeLog(withCalls("a", "", ""), withCalls("b", ""))}, want: schemas.Observed(3, gw)},
		{name: "no tool calls", ev: &schemas.Evidence{Requests: completeLog(okMsg("a"))}, want: schemas.Observed(0, gw)},
		{
			name: "non-message rows ignored",
			ev:   &schemas.Evidence{Requests: completeLog(schemas.GatewayRequest{ID: "x", Kind: "other", ToolCalls: calls("t9")})},
			want: schemas.Observed(0, gw),
		},
		{
			name: "complete gateway wins over stream",
			ev:   &schemas.Evidence{Requests: completeLog(withCalls("a", "t1")), Events: []schemas.Event{streamCall("t1"), streamCall("t2")}},
			want: schemas.Observed(1, gw),
		},
		{
			name: "incomplete gateway falls back to agent stream",
			ev: &schemas.Evidence{Requests: incomplete, Events: []schemas.Event{
				streamCall("t1"), streamCall("t2"), streamCall("t2"),
				{Type: schemas.EventToolResult, Source: schemas.SourceAgentStream, ToolCallID: "t1"},
				{Type: schemas.EventToolCall, Source: schemas.SourceGateway, ToolCallID: "t7"},
			}},
			want: schemas.Observed(2, native),
		},
		{name: "stream calls without ids each count", ev: &schemas.Evidence{Events: []schemas.Event{streamCall(""), streamCall(""), streamCall("t1")}}, want: schemas.Observed(3, native)},
		{name: "no log falls back to agent stream", ev: &schemas.Evidence{Events: []schemas.Event{streamCall("t1")}}, want: schemas.Observed(1, native)},
		{name: "incomplete gateway and no stream", ev: &schemas.Evidence{Requests: incomplete}, want: schemas.Unknown("rows missing", gw)},
		{name: "no evidence", ev: &schemas.Evidence{}, want: schemas.Unknown(reasonNoLog, gw)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoLog, gw)},
	}
	m := newToolCallsMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}
