package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ToolCalls is the name of the requested-tool-call count.
const ToolCalls = "tool_calls"

var _ schemas.Metric = (*toolCallsMetric)(nil)

// toolCallsMetric counts the tool calls the model requested, by distinct
// tool-call id, so parallel calls in one turn count one each and a response
// logged on both a fallback row and its parent counts once.
type toolCallsMetric struct{}

// GetName implements schemas.Part.
func (m *toolCallsMetric) GetName() string { return ToolCalls }

// GetVersion implements schemas.Part.
func (m *toolCallsMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *toolCallsMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           ToolCalls,
		Version:        version,
		Label:          "Tool calls",
		Kind:           schemas.KindCount,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceGateway,
		Description:    "Tool calls the model requested per run, counted by distinct tool-call id, averaged over runs with a complete gateway log or a parsed agent stream.",
		PS2:            true,
	}
}

// Compute implements schemas.Metric. The gateway's content log is the record
// of what the model requested and wins when complete. Without it, the agent's
// own stream is the next-best witness and the value says so with evidence
// native. A tool call with no id cannot be deduplicated, so it counts once
// where it appears.
func (m *toolCallsMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	gatewayGap, bad := requestLogProblem(ev.Requests)
	if !bad {
		ids := make(map[string]struct{})
		for i, r := range ev.Requests.Requests {
			if r.Kind != kindMessage {
				continue
			}
			for j, tc := range r.ToolCalls {
				key := tc.ID
				if key == "" {
					key = fmt.Sprintf("#%d.%d", i, j)
				}
				ids[key] = struct{}{}
			}
		}
		return schemas.Observed(float64(len(ids)), schemas.EvidenceGateway), nil
	}
	ids := make(map[string]struct{})
	for i, e := range ev.Events {
		if e.Type != schemas.EventToolCall || e.Source != schemas.SourceAgentStream {
			continue
		}
		key := e.ToolCallID
		if key == "" {
			key = fmt.Sprintf("#%d", i)
		}
		ids[key] = struct{}{}
	}
	if len(ids) > 0 {
		return schemas.Observed(float64(len(ids)), schemas.EvidenceNative), nil
	}
	return schemas.Unknown(gatewayGap.Reason+" and no agent_stream tool_call events", schemas.EvidenceGateway), nil
}

// newToolCallsMetric returns the tool_calls metric.
func newToolCallsMetric() *toolCallsMetric {
	return &toolCallsMetric{}
}
