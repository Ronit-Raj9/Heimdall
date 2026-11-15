package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestAgentReportedStepMetrics(t *testing.T) {
	rep := func(typ, id, parent, call string) schemas.Event {
		return schemas.Event{Source: schemas.SourceAgentReported, Type: schemas.EventAgentStep, ReportedType: typ, ReportedID: id, ReportedParent: parent, ToolCallID: call}
	}
	ev := &schemas.Evidence{
		AgentEvents: &schemas.AgentEventStatus{Collector: "on", Kept: 8, Dropped: 2},
		Events: []schemas.Event{
			rep("plan", "p", "", ""), rep("subagent_start", "s", "", ""), rep("handoff", "h", "", ""),
			rep("tool_call", "t1", "s", "c1"), rep("tool_result", "r1", "", "c1"), // one call
			rep("tool_result", "r2", "t9", ""), // a result with no reported call: one call
			rep("message", "m", "", ""), rep("llm", "l", "", ""),
			{Source: schemas.SourceAgentStream, Type: schemas.EventToolCall, ToolCallID: "c1"}, // not reported
		},
	}
	want := map[string]float64{ReportedSteps: 5, ReportedToolCalls: 2, SubagentsSpawned: 1, EventsDropped: 2}
	for _, m := range []schemas.Metric{newReportedStepsMetric(), newReportedToolCallsMetric(), newSubagentsSpawnedMetric(), newEventsDroppedMetric()} {
		v, err := m.Compute(context.Background(), nil, ev)
		if err != nil || v.V == nil || *v.V != want[m.GetName()] || v.Evidence != schemas.EvidenceNative || v.Source != schemas.EvidenceSourceAgentReported {
			t.Fatalf("%s = %+v %v, want %v", m.GetName(), v, err, want[m.GetName()])
		}
	}
	// Nothing reported with the collector on: unknown, never zero; dropped
	// is still known.
	empty := &schemas.Evidence{AgentEvents: &schemas.AgentEventStatus{Collector: "on"}}
	if v, _ := newReportedStepsMetric().Compute(context.Background(), nil, empty); v.V != nil || v.Reason != reasonNothingSent {
		t.Fatalf("empty: %+v", v)
	}
	if v, _ := newEventsDroppedMetric().Compute(context.Background(), nil, empty); v.V == nil || *v.V != 0 {
		t.Fatalf("dropped: %+v", v)
	}
	// Collector off and nothing reported: unknown.
	if v, _ := newSubagentsSpawnedMetric().Compute(context.Background(), nil, &schemas.Evidence{}); v.V != nil || v.Reason != reasonNoReports {
		t.Fatalf("off: %+v", v)
	}
}
