package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Names of the agent-reported step metrics (docs/agent-events.md). They read
// only what the agent reported about itself (events with the agent_reported
// source and Evidence.AgentEvents), at native level with partial strength.
// They never feed agent_steps or any trusted metric, and never an outcome.
const (
	ReportedSteps     = "reported_steps"
	ReportedToolCalls = "reported_tool_calls"
	SubagentsSpawned  = "subagents_spawned"
	EventsDropped     = "events_dropped"
)

// Reasons an agent-reported metric has no number.
const (
	reasonNoReports   = "no agent event collector ran and nothing was reported (switched off, or run recorded before agent events)"
	reasonNothingSent = "the agent reported nothing: a run that did nothing and an agent that is not instrumented look the same, so this is unknown, not zero"
)

var (
	_ schemas.Metric = (*reportedMetric)(nil)
)

// reportedMetric is one of the four agent-reported metrics; count reads the
// reported events.
type reportedMetric struct {
	desc  schemas.MetricDescriptor
	count func(ev *schemas.Evidence) float64
	// needEvents: unknown when the agent reported no event at all.
	needEvents bool
}

// GetName implements schemas.Part.
func (m *reportedMetric) GetName() string { return m.desc.Name }

// GetVersion implements schemas.Part.
func (m *reportedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *reportedMetric) Descriptor() schemas.MetricDescriptor { return m.desc }

// Compute implements schemas.Metric.
func (m *reportedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	st := ev.AgentEvents
	if st == nil {
		return schemas.Unknown(reasonNoReports, schemas.EvidenceNative), nil
	}
	if m.needEvents && st.Kept == 0 {
		return schemas.Unknown(reasonNothingSent, schemas.EvidenceNative), nil
	}
	v := schemas.Observed(m.count(ev), schemas.EvidenceNative)
	v.Source, v.EvidenceLevel = schemas.EvidenceSourceAgentReported, schemas.EvidencePartial
	return v, nil
}

// reportedEvents returns the agent-reported events of a run.
func reportedEvents(ev *schemas.Evidence) []schemas.Event {
	var out []schemas.Event
	for _, e := range ev.Events {
		if e.Source == schemas.SourceAgentReported {
			out = append(out, e)
		}
	}
	return out
}

// countReportedTools counts distinct reported tool calls: each tool_call
// event, plus each tool_result that answers no reported tool_call (by tool
// call id or parent id).
func countReportedTools(evs []schemas.Event) int {
	calls, byID := 0, map[string]bool{}
	for _, e := range evs {
		if e.ReportedType == agentevents.TypeToolCall {
			calls++
			if e.ToolCallID != "" {
				byID["call:"+e.ToolCallID] = true
			}
			if e.ReportedID != "" {
				byID["id:"+e.ReportedID] = true
			}
		}
	}
	for _, e := range evs {
		if e.ReportedType != agentevents.TypeToolResult {
			continue
		}
		if (e.ToolCallID != "" && byID["call:"+e.ToolCallID]) || (e.ReportedParent != "" && byID["id:"+e.ReportedParent]) {
			continue
		}
		calls++
	}
	return calls
}

func newReportedStepsMetric() *reportedMetric {
	return &reportedMetric{needEvents: true,
		desc: schemas.MetricDescriptor{
			Name: ReportedSteps, Version: version, Label: "Reported steps (agent's own word)", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceNative,
			Description: "Internal steps the agent reported about itself through OpenTelemetry spans or the native event protocol (plans, subagent runs, handoffs, memory reads and writes, retrievals and distinct tool calls), agent-reported evidence that is never added to agent_steps, averaged over runs whose agent reported at least one event.",
		},
		count: func(ev *schemas.Evidence) float64 {
			evs := reportedEvents(ev)
			n := countReportedTools(evs)
			for _, e := range evs {
				if e.ReportedType != agentevents.TypeToolCall && agentevents.InternalStep(e.ReportedType) {
					n++
				}
			}
			return float64(n)
		},
	}
}

func newReportedToolCallsMetric() *reportedMetric {
	return &reportedMetric{needEvents: true,
		desc: schemas.MetricDescriptor{
			Name: ReportedToolCalls, Version: version, Label: "Reported tool calls", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceNative,
			Description: "Tool calls the agent reported about itself (tool spans and tool_call events, a tool_result with no reported call counting once), including internal tools no gateway or tool proxy sees, as agent-reported evidence averaged over runs whose agent reported at least one event.",
		},
		count: func(ev *schemas.Evidence) float64 { return float64(countReportedTools(reportedEvents(ev))) },
	}
}

func newSubagentsSpawnedMetric() *reportedMetric {
	return &reportedMetric{needEvents: true,
		desc: schemas.MetricDescriptor{
			Name: SubagentsSpawned, Version: version, Label: "Subagents spawned (reported)", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceNative,
			Description: "Subagent runs the agent reported (subagent_start events and agent spans nested in another agent span), as agent-reported evidence averaged over runs whose agent reported at least one event.",
		},
		count: func(ev *schemas.Evidence) float64 {
			n := 0
			for _, e := range reportedEvents(ev) {
				if e.ReportedType == agentevents.TypeSubagentStart {
					n++
				}
			}
			return float64(n)
		},
	}
}

func newEventsDroppedMetric() *reportedMetric {
	return &reportedMetric{
		desc: schemas.MetricDescriptor{
			Name: EventsDropped, Version: version, Label: "Reported events dropped", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceNative,
			Description: "Agent-reported events the bench refused (lines that did not parse, events over the per-run cap, request bodies over the size cap), so a count above zero makes the reported metrics a lower bound, averaged over runs where the collector ran or something was reported.",
		},
		count: func(ev *schemas.Evidence) float64 { return float64(ev.AgentEvents.Dropped) },
	}
}
