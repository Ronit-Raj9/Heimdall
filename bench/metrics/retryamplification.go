package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RetryAmplification is the name of the physical-per-logical attempt ratio.
const RetryAmplification = "retry_amplification"

var _ schemas.Metric = (*retryAmplificationMetric)(nil)

// retryAmplificationMetric shows how many upstream tries each model request
// cost. 1.0 means no retries and no fallbacks.
type retryAmplificationMetric struct{}

// GetName implements schemas.Part.
func (m *retryAmplificationMetric) GetName() string { return RetryAmplification }

// GetVersion implements schemas.Part.
func (m *retryAmplificationMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *retryAmplificationMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           RetryAmplification,
		Version:        version,
		Label:          "Retry amplification",
		Kind:           schemas.KindCount,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceGateway,
		Description:    "Physical upstream attempts divided by logical message requests per run, averaged over runs with a complete gateway log and at least one message request.",
		PS2:            false,
	}
}

// Compute implements schemas.Metric. A row without an attempt trail still went
// upstream once, unless it was a cache hit. Unlike steps, the denominator keeps logical requests that
// only ever failed: those are where the amplification happens.
func (m *retryAmplificationMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	logical := logicalMessageRequests(ev.Requests.Requests)
	if len(logical) == 0 {
		return schemas.NotApplicable(), nil
	}
	physical := 0
	for _, r := range messageRows(ev.Requests.Requests) {
		physical += upstreamAttempts(r)
	}
	return schemas.Observed(float64(physical)/float64(len(logical)), schemas.EvidenceGateway), nil
}

// newRetryAmplificationMetric returns the retry_amplification metric.
func newRetryAmplificationMetric() *retryAmplificationMetric {
	return &retryAmplificationMetric{}
}
