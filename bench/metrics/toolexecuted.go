package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ToolCallsExecuted is the name of the tool execution count.
const ToolCallsExecuted = "tool_calls_executed"

var _ schemas.Metric = (*toolCallsExecutedMetric)(nil)

// toolCallsExecutedVersion is 1.1.0 since tool proxy ledger rows, process
// and workspace observer witnesses count (runs with a step timeline); runs
// without one are computed as in 1.0.0.
const toolCallsExecutedVersion = "1.1.0"

type toolCallsExecutedMetric struct{}

// GetName implements schemas.Part.
func (m *toolCallsExecutedMetric) GetName() string { return ToolCallsExecuted }

// GetVersion implements schemas.Part.
func (m *toolCallsExecutedMetric) GetVersion() string { return toolCallsExecutedVersion }

// Descriptor implements schemas.Metric.
func (m *toolCallsExecutedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ToolCallsExecuted, Version: toolCallsExecutedVersion, Label: "Tool calls executed", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
		Description: "Tool calls with execution evidence per run, each once: requested calls with a witness (tool proxy ledger, shim, process or workspace observer, or the agent's own tool result) plus proxied and shim calls no request explains, averaged over runs with an execution record.",
	}
}

// Compute implements schemas.Metric. A requested call without an execution
// witness is unknown, never zero. Shim records are the boundary witness; a
// tool-result event is a weaker native fallback.
func (m *toolCallsExecutedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if ev.Steps != nil {
		return executedFromTimeline(ev), nil
	}
	requested := len(ev.RequestedTools)
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

// executedFromTimeline counts the timeline's executed tool calls: request
// rows with a witness, and tool proxy, shim or agent-reported executions no
// request explains (counted rows). The value is boundary evidence when a
// trusted witness contributed, native when only the agent said so.
func executedFromTimeline(ev *schemas.Evidence) schemas.Value {
	n, requested, trusted, native := 0, 0, false, false
	for _, s := range ev.Steps {
		switch {
		case s.Kind == schemas.StepToolRequest:
			requested++
			switch s.Execution {
			case schemas.ExecutedWithProof:
				n, trusted = n+1, true
			case schemas.ExecutedAgentReported:
				n, native = n+1, true
			}
		case s.Kind == schemas.StepToolExecution && s.Counted:
			n++
			if s.Source == schemas.StepSourceAgent {
				native = true
			} else {
				trusted = true
			}
		}
	}
	if n == 0 && (requested > 0 || !witnessAvailable(ev)) {
		return schemas.Unknown(fmt.Sprintf("no execution evidence for %d requested tool calls", requested), schemas.EvidenceBoundary)
	}
	if native && !trusted {
		v := schemas.Observed(float64(n), schemas.EvidenceNative)
		v.Source, v.EvidenceLevel = schemas.EvidenceSourceAgentStream, schemas.EvidencePartial
		return v
	}
	v := schemas.Observed(float64(n), schemas.EvidenceBoundary)
	v.Source, v.EvidenceLevel = schemas.EvidenceSourceToolRunner, schemas.EvidenceVerified
	if native {
		v.EvidenceLevel = schemas.EvidencePartial
	}
	return v
}

func newToolCallsExecutedMetric() *toolCallsExecutedMetric { return &toolCallsExecutedMetric{} }
