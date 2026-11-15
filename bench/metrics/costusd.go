package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// CostUSD is the name of the priced spend.
const CostUSD = "cost_usd"

var _ schemas.Metric = (*costUSDMetric)(nil)

// costUSDMetric sums the cost ledger: one row per physical upstream attempt,
// priced from the pinned snapshot. Bifrost's own cost column is a cross-check
// only and is never summed.
type costUSDMetric struct{}

// GetName implements schemas.Part.
func (m *costUSDMetric) GetName() string { return CostUSD }

// GetVersion implements schemas.Part.
func (m *costUSDMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *costUSDMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           CostUSD,
		Version:        version,
		Label:          "Cost (USD)",
		Kind:           schemas.KindUSD,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceGateway,
		Description:    "US dollars per run summed over every physical upstream attempt at pinned prices, averaged over runs whose every attempt was priced.",
		PS2:            true,
	}
}

// Compute implements schemas.Metric. A row that could not be priced makes
// the total unknown. When every unpriced row carries bounds, the total keeps
// bounds from the observed sum to the observed sum plus their upper bounds,
// so the missingness analysis can still use it. A not_applicable row adds
// nothing.
func (m *costUSDMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if len(ev.Cost) == 0 {
		return schemas.Unknown(noCostReason(ev.Requests), schemas.EvidenceGateway), nil
	}
	var observed, upper float64
	unknown := 0
	bounded := true
	for _, row := range ev.Cost {
		if v, ok := row.USD.Float(); ok {
			observed += v
			continue
		}
		if row.USD.State == schemas.ValueNotApplicable {
			continue
		}
		unknown++
		if row.USD.Bounds == nil {
			bounded = false
			continue
		}
		upper += row.USD.Bounds[1]
	}
	if unknown == 0 {
		return schemas.Observed(observed, schemas.EvidenceGateway), nil
	}
	reason := fmt.Sprintf("%d of %d cost rows unpriced", unknown, len(ev.Cost))
	if !bounded {
		return schemas.Unknown(reason, schemas.EvidenceGateway), nil
	}
	return schemas.UnknownWithin(reason, schemas.EvidenceGateway, observed, observed+upper), nil
}

// noCostReason explains a missing cost ledger. Zero rows never mean zero
// spend: either nothing was recorded, or requests exist that were not priced.
func noCostReason(log *schemas.RequestLog) string {
	if log != nil && !log.Complete && log.Reason != "" && len(log.Requests) == 0 {
		// The log itself says why it holds nothing, e.g. a remote agent whose
		// model calls never pass this gateway.
		return log.Reason
	}
	if log == nil || len(log.Requests) == 0 {
		return "no cost evidence"
	}
	return fmt.Sprintf("no cost rows for %d gateway requests", len(log.Requests))
}

// newCostUSDMetric returns the cost_usd metric.
func newCostUSDMetric() *costUSDMetric {
	return &costUSDMetric{}
}
