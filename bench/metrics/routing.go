package metrics

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Routing policy metrics (W9, docs/routing-experiments.md). They read only
// Bifrost's rows (and the priced cost ledger for cost by target), so they
// work for any agent and say what the gateway policy did to a run: whether a
// fallback answered, how often the serving model changed mid-run, and which
// target served what share of the turns, at what cost and latency.
const (
	FallbackUsed        = "fallback_used"
	ServedModelSwitches = "served_model_switches"
	RoutedTarget        = "routed_target"

	// Derived names (one per target that answered a turn). The target is
	// "<provider>/<model>" folded to [a-z0-9_].
	RoutedTargetPrefix  = "routed_target_"     // share of the run's answered turns
	RoutedCostPrefix    = "routed_cost_usd_"   // priced spend on the target, every attempt
	RoutedLatencyPrefix = "routed_latency_ms_" // mean latency of the target's answered rows
)

var (
	_ schemas.Metric        = (*fallbackUsedMetric)(nil)
	_ schemas.Metric        = (*servedModelSwitchesMetric)(nil)
	_ schemas.DerivedMetric = (*routedTargetMetric)(nil)

	targetSlugRe = regexp.MustCompile(`[^a-z0-9]+`)
)

// fallbackUsedMetric is 1 for a run in which a fallback row answered at least
// one model request, else 0.
type fallbackUsedMetric struct{}

// GetName implements schemas.Part.
func (m *fallbackUsedMetric) GetName() string { return FallbackUsed }

// GetVersion implements schemas.Part.
func (m *fallbackUsedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *fallbackUsedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: FallbackUsed, Version: version, Label: "Fallback used", Kind: schemas.KindRate,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Share of runs in which a Bifrost fallback row (fallback_index above 0) answered at least one model request, over runs with a complete gateway log and at least one model request; a routing diagnostic that rises when the primary target fails.",
	}
}

// Compute implements schemas.Metric.
func (m *fallbackUsedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	rows := messageRows(ev.Requests.Requests)
	if len(rows) == 0 {
		return schemas.NotApplicable(), nil
	}
	for _, r := range rows {
		if r.FallbackIndex > 0 && r.Status == statusSuccess {
			return schemas.Observed(1, schemas.EvidenceGateway), nil
		}
	}
	return schemas.Observed(0, schemas.EvidenceGateway), nil
}

// servedModelSwitchesMetric counts the answered model turns served by another
// model than the turn before: the switches a fallback, a weighted split or
// session affinity made mid-run.
type servedModelSwitchesMetric struct{}

// GetName implements schemas.Part.
func (m *servedModelSwitchesMetric) GetName() string { return ServedModelSwitches }

// GetVersion implements schemas.Part.
func (m *servedModelSwitchesMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *servedModelSwitchesMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ServedModelSwitches, Version: version, Label: "Served model switches", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Answered model turns per run whose serving model (Bifrost row, response model) differs from the previous answered turn's, averaged over runs with a complete gateway log and at least one answered turn (failed turns are skipped, so they neither hide nor invent a switch).",
	}
}

// Compute implements schemas.Metric.
func (m *servedModelSwitchesMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	served := answeredTargets(ev.Requests.Requests)
	if len(served) == 0 {
		return schemas.NotApplicable(), nil
	}
	n := 0
	for i := 1; i < len(served); i++ {
		if bareModelID(served[i].row.ModelServed) != bareModelID(served[i-1].row.ModelServed) {
			n++
		}
	}
	return schemas.Observed(float64(n), schemas.EvidenceGateway), nil
}

// routedTargetMetric is the number of distinct targets (provider and model)
// that answered a run's turns; through ComputeDerived it reports, per target,
// the share of turns, the priced cost and the mean latency.
type routedTargetMetric struct{}

// GetName implements schemas.Part.
func (m *routedTargetMetric) GetName() string { return RoutedTarget }

// GetVersion implements schemas.Part.
func (m *routedTargetMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *routedTargetMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: RoutedTarget, Version: version, Label: "Targets served", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Distinct provider/model targets that answered at least one model turn per run, averaged over runs with a complete gateway log and an answered turn; per target, routed_target_<target> is its share of answered turns, routed_cost_usd_<target> its priced spend (every attempt) and routed_latency_ms_<target> its mean answered-row latency, all report-only.",
	}
}

// Compute implements schemas.Metric.
func (m *routedTargetMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	served := answeredTargets(ev.Requests.Requests)
	if len(served) == 0 {
		return schemas.NotApplicable(), nil
	}
	distinct := map[string]bool{}
	for _, s := range served {
		distinct[s.target] = true
	}
	return schemas.Observed(float64(len(distinct)), schemas.EvidenceGateway), nil
}

// ComputeDerived implements schemas.DerivedMetric. Only a run whose agent
// config declares a routing policy gets per-target values, so experiments of
// fixed keys gain no rows. An incomplete log or a run with no answered turn
// yields none; a target whose attempts are not all priced has an unknown
// cost, never 0.
func (m *routedTargetMetric) ComputeDerived(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (map[string]schemas.Value, error) {
	if rc == nil || rc.Agent == nil || rc.Agent.Gateway.Routing == nil {
		return nil, nil
	}
	ev = orEmpty(ev)
	if _, bad := requestLogProblem(ev.Requests); bad {
		return nil, nil
	}
	served := answeredTargets(ev.Requests.Requests)
	if len(served) == 0 {
		return nil, nil
	}
	out := map[string]schemas.Value{}
	turns := map[string]int{}
	latency := map[string][]float64{}
	for _, s := range served {
		turns[s.target]++
		latency[s.target] = append(latency[s.target], s.row.LatencyMS)
	}
	for t, n := range turns {
		slug := TargetSlug(t)
		out[RoutedTargetPrefix+slug] = schemas.Observed(float64(n)/float64(len(served)), schemas.EvidenceGateway)
		out[RoutedLatencyPrefix+slug] = schemas.Observed(mean(latency[t]), schemas.EvidenceGateway)
	}
	for t, v := range costByTarget(ev) {
		out[RoutedCostPrefix+TargetSlug(t)] = v
	}
	return out, nil
}

// TargetSlug folds a "<provider>/<model>" target into a metric name suffix:
// lower case, every run of other characters one "_".
func TargetSlug(target string) string {
	return strings.Trim(targetSlugRe.ReplaceAllString(strings.ToLower(target), "_"), "_")
}

// servedTurn is one answered logical model request and the row that answered.
type servedTurn struct {
	target string // provider/model of the answering row
	row    schemas.GatewayRequest
}

// answeredTargets returns the answered logical model requests in log order,
// each with the last successful row of the request (the fallback that
// answered, when one did).
func answeredTargets(reqs []schemas.GatewayRequest) []servedTurn {
	var out []servedTurn
	for _, l := range logicalMessageRequests(reqs) {
		var last *schemas.GatewayRequest
		for i := range l.rows {
			if l.rows[i].Status == statusSuccess {
				last = &l.rows[i]
			}
		}
		if last != nil {
			out = append(out, servedTurn{target: rowTarget(*last), row: *last})
		}
	}
	return out
}

// costByTarget sums the priced cost ledger per target of the row each cost
// row belongs to. A target with an unpriced attempt is unknown; an injected
// fault (billed as zero) is skipped.
func costByTarget(ev *schemas.Evidence) map[string]schemas.Value {
	if len(ev.Cost) == 0 {
		return nil
	}
	byID := map[string]string{}
	for _, r := range ev.Requests.Requests {
		byID[r.ID] = rowTarget(r)
	}
	sums, unknown := map[string]float64{}, map[string]bool{}
	for _, c := range ev.Cost {
		t, ok := byID[c.RequestID]
		if !ok {
			continue
		}
		if v, ok := c.USD.Float(); ok {
			sums[t] += v
			continue
		}
		if c.USD.State != schemas.ValueNotApplicable && !c.FaultInject {
			unknown[t] = true
		}
	}
	out := map[string]schemas.Value{}
	targets := make([]string, 0, len(sums)+len(unknown))
	for t := range sums {
		targets = append(targets, t)
	}
	for t := range unknown {
		if _, ok := sums[t]; !ok {
			targets = append(targets, t)
		}
	}
	sort.Strings(targets)
	for _, t := range targets {
		if unknown[t] {
			out[t] = schemas.Unknown("unpriced attempt on target "+t, schemas.EvidenceGateway)
			continue
		}
		out[t] = schemas.Observed(sums[t], schemas.EvidenceGateway)
	}
	return out
}

// rowTarget is "<provider>/<model>" of a row, the model being the one that
// served it (bare, without a provider segment).
func rowTarget(r schemas.GatewayRequest) string {
	model := r.ModelServed
	if model == "" {
		model = r.ModelRequested
	}
	return r.Provider + "/" + bareModelID(model)
}

// bareModelID drops one leading provider segment ("anthropic/claude-x").
func bareModelID(m string) string {
	if _, rest, ok := strings.Cut(m, "/"); ok {
		return rest
	}
	return m
}

// mean is the arithmetic mean of xs; 0 for none.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func newFallbackUsedMetric() *fallbackUsedMetric               { return &fallbackUsedMetric{} }
func newServedModelSwitchesMetric() *servedModelSwitchesMetric { return &servedModelSwitchesMetric{} }
func newRoutedTargetMetric() *routedTargetMetric               { return &routedTargetMetric{} }
