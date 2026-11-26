package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestToolCallsGatewayZeroStreamWins: tools that run inside the agent never
// reach the gateway, so a complete gateway log with no tool calls must not
// beat the agent's own stream (the overview said 0 while the timeline listed
// 38). A gateway that did see calls still wins.
func TestToolCallsGatewayZeroStreamWins(t *testing.T) {
	streamCall := func(id string) schemas.Event {
		return schemas.Event{Type: schemas.EventToolCall, Source: schemas.SourceAgentStream, ToolCallID: id}
	}
	m := newToolCallsMetric()
	ev := &schemas.Evidence{Requests: completeLog(okMsg("a")), Events: []schemas.Event{streamCall("t1"), streamCall("t2"), streamCall("t3")}}
	checkValue(t, compute(t, m, nil, ev), schemas.Observed(3, schemas.EvidenceNative))

	withCalls := okMsg("b")
	withCalls.ToolCalls = calls("g1", "g2")
	ev = &schemas.Evidence{Requests: completeLog(withCalls), Events: []schemas.Event{streamCall("t1"), streamCall("t2"), streamCall("t3")}}
	checkValue(t, compute(t, m, nil, ev), schemas.Observed(2, schemas.EvidenceGateway))

	ev = &schemas.Evidence{Requests: completeLog(okMsg("c"))}
	checkValue(t, compute(t, m, nil, ev), schemas.Observed(0, schemas.EvidenceGateway))
}
