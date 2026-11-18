package metrics

import (
	"context"
	"math"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// B1: metrics of an a2a run, from Bifrost's agent log (Evidence.A2A,
// docs/a2a-agents.md). All are lower-is-better counts or durations witnessed
// by the gateway; none applies to an agent that is not an a2a agent.
const (
	A2ATasks     = "a2a_tasks"
	A2ALatencyMS = "a2a_latency_ms"
	A2AErrors    = "a2a_errors"
	A2AHops      = "a2a_hops"
)

const (
	reasonNoA2AWitness  = "no agent gateway witness for the run (no registration, or the agent log could not be read)"
	reasonA2AIncomplete = "the agent log does not hold every operation the launcher made"
	reasonNoA2ALatency  = "the agent log recorded no latency for the bench's operations"
)

var _ schemas.Metric = (*a2aMetric)(nil)

// a2aMetric is one of the four a2a metrics.
type a2aMetric struct{ name string }

func newA2AMetric(name string) *a2aMetric { return &a2aMetric{name: name} }

// GetName implements schemas.Part.
func (m *a2aMetric) GetName() string { return m.name }

// GetVersion implements schemas.Part.
func (m *a2aMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *a2aMetric) Descriptor() schemas.MetricDescriptor {
	d := schemas.MetricDescriptor{Name: m.name, Version: version, Kind: schemas.KindCount, HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway}
	switch m.name {
	case A2ATasks:
		d.Label = "A2A tasks"
		d.Description = "Distinct A2A tasks the run's agent gateway operations belonged to, across the entry agent and its team, as Bifrost's agent log recorded them, averaged over runs of a2a agents."
	case A2ALatencyMS:
		d.Label = "A2A latency (ms)"
		d.Description = "Milliseconds from the start of the first agent gateway operation the bench sent to the end of the last one, from the timestamps and latencies in Bifrost's agent log, averaged over runs of a2a agents."
	case A2AErrors:
		d.Label = "A2A errors"
		d.Description = "Agent gateway operations of the run that Bifrost's agent log recorded as errors, plus A2A tasks whose recorded final state is failed or rejected (a successful operation can return a failed task), averaged over runs of a2a agents."
	case A2AHops:
		d.Label = "A2A agent-to-agent hops"
		d.Description = "Messages one agent of the run sent another through the agent gateway, each proven by the key Bifrost's agent log says it came in on (only that agent holds it), averaged over runs of a2a agents."
	}
	return d
}

// Compute implements schemas.Metric.
func (m *a2aMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if rc == nil || !utils.IsA2A(rc.Agent) {
		return schemas.NotApplicable(), nil
	}
	if ev == nil || ev.A2A == nil || len(ev.A2A.Operations) == 0 {
		return schemas.Unknown(reasonNoA2AWitness, schemas.EvidenceGateway), nil
	}
	a := ev.A2A
	switch m.name {
	case A2ATasks:
		seen := map[string]bool{}
		for _, op := range a.Operations {
			if op.TaskID != "" {
				seen[op.TaskID] = true
			}
		}
		return a2aValue(float64(len(seen)), a), nil
	case A2ALatencyMS:
		start, end := math.Inf(1), math.Inf(-1)
		for _, op := range a.Operations {
			if op.Caller != "bench" || op.LatencyMS == nil {
				continue
			}
			t := float64(op.Timestamp.UnixNano()) / 1e6
			start, end = math.Min(start, t), math.Max(end, t+*op.LatencyMS)
		}
		if math.IsInf(start, 1) {
			return schemas.Unknown(reasonNoA2ALatency, schemas.EvidenceGateway), nil
		}
		return a2aValue(math.Round(end-start), a), nil
	case A2AErrors:
		n := 0
		for _, op := range a.Operations {
			if op.Status == "error" {
				n++
			}
		}
		for _, st := range a.TaskStates {
			if st == "TASK_STATE_FAILED" || st == "TASK_STATE_REJECTED" {
				n++
			}
		}
		return a2aValue(float64(n), a), nil
	default:
		return a2aValue(float64(len(a.Hops)), a), nil
	}
}

// a2aValue is an observed gateway value, or unknown within [v, inf) when
// the witness is incomplete: what the log holds is a lower bound only for
// counts, so an incomplete witness reports unknown with the reason.
func a2aValue(v float64, a *schemas.A2AEvidence) schemas.Value {
	if !a.Complete {
		return schemas.Unknown(reasonA2AIncomplete+": "+a.Reason, schemas.EvidenceGateway)
	}
	out := schemas.Observed(v, schemas.EvidenceGateway)
	out.Source = schemas.EvidenceSourceBifrostLog
	return out
}
