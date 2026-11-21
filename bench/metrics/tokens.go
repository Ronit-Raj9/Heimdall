package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Tokens is the name of the token total.
const Tokens = "tokens"

var _ schemas.Metric = (*tokensMetric)(nil)

// tokensMetric totals the four disjoint token buckets over every message row.
type tokensMetric struct{}

// GetName implements schemas.Part.
func (m *tokensMetric) GetName() string { return Tokens }

// GetVersion implements schemas.Part.
func (m *tokensMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *tokensMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           Tokens,
		Version:        version,
		Label:          "Tokens",
		Kind:           schemas.KindTokens,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceGateway,
		Description:    "Tokens per run as fresh input plus cache read plus cache write plus output over every message request and every audio row that records tokens (Realtime turns), averaged over runs whose every message request recorded usage.",
		PS2:            true,
	}
}

// Compute implements schemas.Metric. One row without usage makes the total
// unknown: the buckets are summed only when every one of them was recorded,
// so a partial sum is never shown as the run's total. An error row without
// usage is skipped, as the usage tally and input_tokens skip it: a refused,
// failed or injected request consumed no tokens, so a fallback or outage run
// still has a known total.
func (m *tokensMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	var sum int64
	for _, r := range messageRows(ev.Requests.Requests) {
		if r.Status == "error" && r.Tokens == nil {
			continue // a refused, failed or injected request has no usage to record (RequestLog.Tally, inputSplit)
		}
		if r.Tokens == nil {
			return schemas.Unknown(fmt.Sprintf("message request %s has no usage", r.ID), schemas.EvidenceGateway), nil
		}
		sum += r.Tokens.Fresh + r.Tokens.CacheRead + r.Tokens.CacheWrite + r.Tokens.Out
	}
	for _, r := range ev.Requests.Requests { // X8 voice agents: Realtime turns carry tokens; speech and transcription rows are priced by audio and carry none
		if r.Kind == kindAudio && r.Tokens != nil {
			sum += r.Tokens.Fresh + r.Tokens.CacheRead + r.Tokens.CacheWrite + r.Tokens.Out
		}
	}
	return schemas.Observed(float64(sum), schemas.EvidenceGateway), nil
}

// newTokensMetric returns the tokens metric.
func newTokensMetric() *tokensMetric {
	return &tokensMetric{}
}
