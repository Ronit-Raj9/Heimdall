package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Names of the metrics read from the external API rows of the tool proxy's
// ledger (schemas.ToolCall with Protocol external, docs/external-apis.md).
const (
	ExternalCalls     = "external_calls"
	ExternalUnmatched = "external_unmatched"
	ExternalErrorRate = "external_error_rate"
)

// protocolExternal is the ledger protocol of an external API call. It
// mirrors toolproxy.ProtocolExternal, which this package does not import.
const protocolExternal = "external"

// External API calls are recorded by the same proxy as tool calls, at the
// same boundary. A task without an external API has none to read, so the
// values do not apply; a task with one whose ledger is missing has values
// that could not be observed, so they are unknown, never zero.

var (
	_ schemas.Metric = (*externalCallsMetric)(nil)
	_ schemas.Metric = (*externalUnmatchedMetric)(nil)
	_ schemas.Metric = (*externalErrorRateMetric)(nil)
)

// externalCallsMetric counts the calls the agent made to external APIs.
type externalCallsMetric struct{}

// GetName implements schemas.Part.
func (m *externalCallsMetric) GetName() string { return ExternalCalls }

// GetVersion implements schemas.Part.
func (m *externalCallsMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric. Lower is better, like
// tool_calls_proxied: the verifier judges the outcome, and fewer calls to a
// real API reach it more cheaply; the number is a diagnostic.
func (m *externalCallsMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           ExternalCalls,
		Version:        version,
		Label:          "External API calls",
		Kind:           schemas.KindCount,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "Calls the agent made to the run's external APIs through the tool proxy (allowed, refused, replayed and unmatched alike), averaged over runs of tasks with external APIs.",
	}
}

// Compute implements schemas.Metric.
func (m *externalCallsMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	calls, v, ok := externalCalls(rc, ev)
	if !ok {
		return v, nil
	}
	return schemas.Observed(float64(len(calls)), schemas.EvidenceBoundary), nil
}

// newExternalCallsMetric returns the external_calls metric.
func newExternalCallsMetric() *externalCallsMetric { return &externalCallsMetric{} }

// externalUnmatchedMetric counts replay-mode calls the cassette had no
// answer for. The agent asked for something the recording never saw: it is
// the agent's call, not an infrastructure failure, and this count makes it
// visible.
type externalUnmatchedMetric struct{}

// GetName implements schemas.Part.
func (m *externalUnmatchedMetric) GetName() string { return ExternalUnmatched }

// GetVersion implements schemas.Part.
func (m *externalUnmatchedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *externalUnmatchedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           ExternalUnmatched,
		Version:        version,
		Label:          "Unmatched external calls",
		Kind:           schemas.KindCount,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "External API calls in replay mode that the task's cassette held no recorded answer for (the agent got a 599), averaged over runs of tasks with external APIs.",
	}
}

// Compute implements schemas.Metric.
func (m *externalUnmatchedMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	calls, v, ok := externalCalls(rc, ev)
	if !ok {
		return v, nil
	}
	n := 0
	for _, c := range calls {
		if c.Unmatched {
			n++
		}
	}
	return schemas.Observed(float64(n), schemas.EvidenceBoundary), nil
}

// newExternalUnmatchedMetric returns the external_unmatched metric.
func newExternalUnmatchedMetric() *externalUnmatchedMetric { return &externalUnmatchedMetric{} }

// externalErrorRateMetric is the share of external API calls that failed as
// the agent saw them: error statuses, refused calls, unmatched replays,
// unreachable hosts and injected faults alike.
type externalErrorRateMetric struct{}

// GetName implements schemas.Part.
func (m *externalErrorRateMetric) GetName() string { return ExternalErrorRate }

// GetVersion implements schemas.Part.
func (m *externalErrorRateMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *externalErrorRateMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           ExternalErrorRate,
		Version:        version,
		Label:          "External API error rate",
		Kind:           schemas.KindRate,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "Share of the run's external API calls that failed (error statuses, calls the proxy refused, unmatched replays, unreachable hosts and injected faults), averaged over runs that made at least one external call.",
	}
}

// Compute implements schemas.Metric. A run with no external call has no
// rate: not applicable.
func (m *externalErrorRateMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	calls, v, ok := externalCalls(rc, ev)
	if !ok {
		return v, nil
	}
	if len(calls) == 0 {
		return schemas.NotApplicable(), nil
	}
	failed := 0
	for _, c := range calls {
		if callFailed(c) {
			failed++
		}
	}
	return schemas.Observed(float64(failed)/float64(len(calls)), schemas.EvidenceBoundary), nil
}

// newExternalErrorRateMetric returns the external_error_rate metric.
func newExternalErrorRateMetric() *externalErrorRateMetric { return &externalErrorRateMetric{} }

// externalCalls returns the ledger's external API rows, or the value to
// report instead and false: not applicable for a task without external
// APIs, unknown when the ledger is missing.
func externalCalls(rc *schemas.RunContext, ev *schemas.Evidence) ([]schemas.ToolCall, schemas.Value, bool) {
	if !hasExternalAPI(rc) {
		return nil, schemas.NotApplicable(), false
	}
	ev = orEmpty(ev)
	if ev.ToolCalls == nil {
		return nil, schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary), false
	}
	calls := []schemas.ToolCall{}
	for _, c := range ev.ToolCalls {
		if c.Protocol == protocolExternal {
			calls = append(calls, c)
		}
	}
	return calls, schemas.Value{}, true
}

// hasExternalAPI reports whether the run's suite or task declares an
// external API.
func hasExternalAPI(rc *schemas.RunContext) bool {
	if rc == nil {
		return false
	}
	return (rc.Suite != nil && len(rc.Suite.External) > 0) || (rc.Task != nil && len(rc.Task.External) > 0)
}
