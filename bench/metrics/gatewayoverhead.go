package metrics

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Gateway overhead (B2, docs/bifrost-features.md): the time Bifrost itself
// adds to a run's model requests, read from its own rows. Bifrost v2.5.1
// records per row the total latency, the upstream latency and the
// difference, overhead_latency, which is time spent inside the gateway
// (plugins, governance, routing, request conversion, queueing). The two
// metrics are always on and cost nothing: every run through Bifrost carries
// them, whatever the agent.
const (
	GatewayOverheadMS    = "gateway_overhead_ms"
	GatewayOverheadP95MS = "gateway_overhead_p95_ms"
)

var _ schemas.Metric = (*gatewayOverheadMetric)(nil)

// gatewayOverheadMetric is the median or the 95th percentile of the
// overhead of a run's message rows.
type gatewayOverheadMetric struct {
	name string
	p95  bool
}

func newGatewayOverheadMetric(name string) *gatewayOverheadMetric {
	return &gatewayOverheadMetric{name: name, p95: name == GatewayOverheadP95MS}
}

// GetName implements schemas.Part.
func (m *gatewayOverheadMetric) GetName() string { return m.name }

// GetVersion implements schemas.Part.
func (m *gatewayOverheadMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *gatewayOverheadMetric) Descriptor() schemas.MetricDescriptor {
	if m.p95 {
		return schemas.MetricDescriptor{
			Name: m.name, Version: version, Label: "Gateway overhead p95 (ms)", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
			Description: "95th percentile (nearest rank) of Bifrost's own overhead_latency in ms (the time the gateway itself adds to its slowest requests) over the run's model request rows, averaged over runs with a complete gateway log whose rows record the overhead.",
		}
	}
	return schemas.MetricDescriptor{
		Name: m.name, Version: version, Label: "Gateway overhead (ms)", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Median of Bifrost's own overhead_latency in ms (latency minus upstream_latency as the gateway logs it: time inside the gateway, never the provider) over the run's model request rows, averaged over runs with a complete gateway log whose rows record the overhead.",
	}
}

// Compute implements schemas.Metric.
func (m *gatewayOverheadMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	rows := messageRows(ev.Requests.Requests)
	if len(rows) == 0 {
		return schemas.NotApplicable(), nil
	}
	vals := make([]float64, 0, len(rows))
	for _, r := range rows {
		if r.OverheadMS != nil && !math.IsNaN(*r.OverheadMS) {
			vals = append(vals, *r.OverheadMS)
		}
	}
	if len(vals) == 0 {
		return schemas.Unknown("no model request row records Bifrost's overhead_latency (a gateway older than v2.5.1, or a run sealed before B2)", schemas.EvidenceGateway), nil
	}
	x := stats.Median(vals)
	if m.p95 {
		x = nearestRank(vals, 0.95)
	}
	v := schemas.Observed(round3(x), schemas.EvidenceGateway)
	if len(vals) < len(rows) {
		v.Reason = fmt.Sprintf("%d of %d model rows record the overhead", len(vals), len(rows))
	}
	return v, nil
}

// nearestRank is the q quantile of vals by the nearest-rank rule: the
// smallest value with at least q of the values at or below it.
func nearestRank(vals []float64, q float64) float64 {
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	k := int(math.Ceil(q*float64(len(s)))) - 1
	if k < 0 {
		k = 0
	}
	return s[k]
}

// round3 keeps three decimals, so a sealed value reads like the gateway's.
func round3(x float64) float64 { return math.Round(x*1000) / 1000 }
