package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ToolCallsExecuted is the name of the tool execution count.
const ToolCallsExecuted = "tool_calls_executed"

var _ schemas.Metric = (*toolCallsExecutedMetric)(nil)

type toolCallsExecutedMetric struct{}

// GetName implements schemas.Part.
func (m *toolCallsExecutedMetric) GetName() string { return ToolCallsExecuted }

// GetVersion implements schemas.Part.
func (m *toolCallsExecutedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *toolCallsExecutedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ToolCallsExecuted, Version: version, Label: "Tool calls executed", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
		Description: "Tool calls with execution evidence per run, averaged over runs with an execution record.",
	}
}

// Compute implements schemas.Metric. A requested call without an execution
// witness is unknown, never zero. Shim records are the boundary witness; a
// tool-result event is a weaker native fallback.
func (m *toolCallsExecutedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	requested := len(ev.ToolCalls)
	if requested == 0 && ev.Requests != nil && ev.Requests.Complete {
		for _, row := range ev.Requests.Requests {
			if row.Kind == kindMessage {
				requested += len(row.ToolCalls)
			}
		}
	}
	if ev.ShimLog != nil {
		ids := map[string]struct{}{}
		for i, call := range ev.ShimLog {
			if call.Action == "" {
				continue
			}
			id := call.ToolCallID
			if id == "" {
				id = call.ExecutionID
			}
			if id == "" {
				id = fmt.Sprintf("%s:%d:%d", call.Tool, call.Call, i)
			}
			ids[id] = struct{}{}
		}
		v := schemas.Observed(float64(len(ids)), schemas.EvidenceBoundary)
		v.Source, v.EvidenceLevel = schemas.EvidenceSourceToolRunner, schemas.EvidenceVerified
		return v, nil
	}
	ids := map[string]struct{}{}
	for i, event := range ev.Events {
		if event.Type != schemas.EventToolResult {
			continue
		}
		id := event.ToolCallID
		if id == "" {
			id = fmt.Sprintf("event:%d", i)
		}
		ids[id] = struct{}{}
	}
	if len(ids) > 0 {
		v := schemas.Observed(float64(len(ids)), schemas.EvidenceNative)
		v.Source, v.EvidenceLevel = schemas.EvidenceSourceAgentStream, schemas.EvidencePartial
		return v, nil
	}
	return schemas.Unknown(fmt.Sprintf("no execution evidence for %d requested tool calls", requested), schemas.EvidenceBoundary), nil
}

func newToolCallsExecutedMetric() *toolCallsExecutedMetric { return &toolCallsExecutedMetric{} }
