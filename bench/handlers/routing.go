package handlers

import (
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Routing policy under test (W9, docs/routing-experiments.md): the Gateway
// policy card of the verdict and run pages. Everything comes from the frozen
// manifest and the sealed runs' metrics.json, gates.json and evidence.json.

// Metric names the routing views read (bench/metrics/routing.go). The
// package may not import metrics, so they are restated here.
const (
	routingFallbackUsed    = "fallback_used"
	routingSwitches        = "served_model_switches"
	routingTargetPrefix    = "routed_target_"
	routingCostPrefix      = "routed_cost_usd_"
	routingLatencyPrefix   = "routed_latency_ms_"
	routingFidelityGate    = "model_fidelity"
	routingDefaultProvider = "anthropic"
)

// routingSlugRe folds a target into a metric suffix, as metrics.TargetSlug.
var routingSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// RoutingHandler serves the gateway policy views.
type RoutingHandler struct {
	mode  schemas.Mode
	index ReadIndex
}

// NewRoutingHandler creates the routing handler. index may be nil.
func NewRoutingHandler(mode schemas.Mode, index ReadIndex) *RoutingHandler {
	return &RoutingHandler{mode: mode, index: index}
}

// RegisterRoutes registers GET /experiments/{id}/routing and
// GET /runs/{id}/routing.
func (h *RoutingHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/routing", lib.ChainMiddlewares(h.getExperimentRouting, middlewares...))
	r.GET(APIPrefix+"/runs/{id}/routing", lib.ChainMiddlewares(h.getRunRouting, middlewares...))
}

// getExperimentRouting answers every arm's policy, the lines on which the
// arms' policies differ, and per arm and condition what the policy did.
func (h *RoutingHandler) getExperimentRouting(ctx *fasthttp.RequestCtx) {
	expID, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	m, found := h.index.Manifest(expID)
	if !found {
		sendNotFound(ctx, h.mode, "experiment", expID)
		return
	}
	out, err := ExperimentRouting(h.index, m)
	if err != nil {
		sendDepError(ctx, h.mode, "routing of experiment "+expID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// getRunRouting answers one run's policy, its gateway rows and its model
// fidelity result.
func (h *RoutingHandler) getRunRouting(ctx *fasthttp.RequestCtx) {
	runID, ok := idParam(ctx, h.mode, "id", "run")
	if !ok {
		return
	}
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	rv, found := h.index.Run(runID)
	if !found {
		sendNotFound(ctx, h.mode, "run", runID)
		return
	}
	out, err := runRouting(h.index, rv)
	if err != nil {
		sendDepError(ctx, h.mode, "routing of run "+runID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// ExperimentRouting builds the experiment's routing summary over its sealed
// counted runs.
func ExperimentRouting(index ReadIndex, m *schemas.Manifest) (*schemas.RoutingSummary, error) {
	out := &schemas.RoutingSummary{ExpID: m.ExpID, Arms: []schemas.RoutingArm{}, Differences: []string{}}
	byArm := map[string]map[string][]map[string]schemas.Value{} // arm -> condition -> metrics per run
	for _, rv := range countedRuns(index.Runs(m.ExpID)) {
		if !rv.Sealed || !rv.Slot.Counted {
			continue
		}
		metrics, err := readMetrics(index, rv.Run.RunID)
		if err != nil {
			return nil, err
		}
		if byArm[rv.Slot.Arm] == nil {
			byArm[rv.Slot.Arm] = map[string][]map[string]schemas.Value{}
		}
		byArm[rv.Slot.Arm][rv.Slot.Condition] = append(byArm[rv.Slot.Arm][rv.Slot.Condition], metrics)
	}
	ids := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		a := m.Arms[id]
		pol := a.Config.Gateway.Routing
		out.Routed = out.Routed || pol != nil
		arm := schemas.RoutingArm{Arm: id, Label: a.Label, Model: a.Config.Model, Policy: pol, PolicyHash: m.Routing[id],
			Summary: schemas.RoutingSummaryLine(&a.Config), Conditions: []schemas.RoutingCondition{}}
		conds := make([]string, 0, len(byArm[id]))
		for c := range byArm[id] {
			conds = append(conds, c)
		}
		slices.Sort(conds)
		for _, c := range conds {
			arm.Conditions = append(arm.Conditions, routingCondition(c, byArm[id][c], &a.Config))
		}
		out.Arms = append(out.Arms, arm)
	}
	out.Differences = schemas.RoutingDifferences(out.Arms)
	return out, nil
}

// runRouting builds one run's routing view.
func runRouting(index ReadIndex, rv *RunView) (*schemas.RunRouting, error) {
	out := &schemas.RunRouting{RunID: rv.Run.RunID, Arm: rv.Slot.Arm, Condition: rv.Slot.Condition,
		Targets: []schemas.RoutingTargetUse{}, Rows: []schemas.RunRoutingRow{}}
	var cfg *schemas.AgentConfig
	if m, ok := index.Manifest(rv.Slot.ExpID); ok {
		if a, ok := m.Arms[rv.Slot.Arm]; ok {
			cfg = &a.Config
		} else if rv.Slot.Arm == "compat" && m.Compatibility != nil {
			cfg = &m.Compatibility.Config
		}
	}
	if cfg != nil {
		out.Model, out.Policy, out.Summary = cfg.Model, cfg.Gateway.Routing, schemas.RoutingSummaryLine(cfg)
	}
	metrics, err := readMetrics(index, rv.Run.RunID)
	if err != nil {
		return nil, err
	}
	if v, ok := metrics[routingFallbackUsed].Float(); ok {
		used := v > 0
		out.FallbackUsed = &used
	}
	if v, ok := metrics[routingSwitches].Float(); ok {
		n := int(v)
		out.Switches = &n
	}
	out.Targets = routingTargets([]map[string]schemas.Value{metrics}, cfg)
	var ev struct {
		Requests *schemas.RequestLog `json:"requests"`
	}
	data, found, err := readInternal(index, rv.Run.RunID, FileEvidence) // read for its gateway rows, never served raw
	if err != nil {
		return nil, err
	}
	if found {
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil, fmt.Errorf("parse %s of run %s: %w", FileEvidence, rv.Run.RunID, err)
		}
	}
	if ev.Requests != nil {
		for _, r := range ev.Requests.Requests {
			if r.Kind != "message" {
				continue
			}
			out.Rows = append(out.Rows, schemas.RunRoutingRow{ID: r.ID, ParentID: r.ParentID, Provider: r.Provider, ModelServed: r.ModelServed,
				FallbackIndex: r.FallbackIndex, Status: r.Status, ErrorStatus: r.ErrorStatus, LatencyMS: r.LatencyMS,
				RoutingRuleName: r.RoutingRuleName, RoutingEngines: r.RoutingEngines})
		}
	}
	var gates []schemas.GateResult
	if _, err := readJSONFile(index, rv.Run.RunID, FileGates, &gates); err != nil {
		return nil, err
	}
	for i := range gates {
		if gates[i].Gate == routingFidelityGate {
			out.Fidelity = &gates[i]
		}
	}
	return out, nil
}

// routingCondition aggregates one arm's runs under one condition.
func routingCondition(cond string, runs []map[string]schemas.Value, cfg *schemas.AgentConfig) schemas.RoutingCondition {
	c := schemas.RoutingCondition{Condition: cond, Runs: len(runs)}
	for _, m := range runs {
		if v, ok := m[routingFallbackUsed].Float(); ok {
			c.FallbackKnown++
			if v > 0 {
				c.FallbackRuns++
			}
		}
		if v, ok := m[routingSwitches].Float(); ok {
			c.SwitchesKnown++
			c.Switches += int(v)
		}
	}
	c.Targets = routingTargets(runs, cfg)
	return c
}

// routingTargets averages the per-target turn share and latency and sums
// the cost over runs, from the routed_* derived metrics. A target is named
// provider/model when the arm's policy (or its model) names it, else by its
// metric suffix. A cost that was unknown in any run is left out.
func routingTargets(runs []map[string]schemas.Value, cfg *schemas.AgentConfig) []schemas.RoutingTargetUse {
	names := routingTargetNames(cfg)
	type acc struct {
		share, latency          float64
		shareN, latencyN, costN int
		cost                    float64
		costBad                 bool
	}
	by := map[string]*acc{}
	get := func(slug string) *acc {
		if by[slug] == nil {
			by[slug] = &acc{}
		}
		return by[slug]
	}
	reported := 0
	for _, m := range runs {
		seen := false
		for name, v := range m {
			switch {
			case strings.HasPrefix(name, routingCostPrefix):
				a := get(strings.TrimPrefix(name, routingCostPrefix))
				if f, ok := v.Float(); ok {
					a.cost += f
					a.costN++
				} else {
					a.costBad = true
				}
			case strings.HasPrefix(name, routingLatencyPrefix):
				if f, ok := v.Float(); ok {
					a := get(strings.TrimPrefix(name, routingLatencyPrefix))
					a.latency += f
					a.latencyN++
				}
			case strings.HasPrefix(name, routingTargetPrefix):
				if f, ok := v.Float(); ok {
					a := get(strings.TrimPrefix(name, routingTargetPrefix))
					a.share += f
					a.shareN++
					seen = true
				}
			}
		}
		if seen {
			reported++
		}
	}
	out := []schemas.RoutingTargetUse{}
	for slug, a := range by {
		t := schemas.RoutingTargetUse{Target: cmp.Or(names[slug], slug)}
		if reported > 0 {
			t.TurnShare = a.share / float64(reported)
		}
		if a.costN > 0 && !a.costBad {
			cost := a.cost
			t.CostUSD = &cost
		}
		if a.latencyN > 0 {
			lat := a.latency / float64(a.latencyN)
			t.LatencyMS = &lat
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(x, y schemas.RoutingTargetUse) int {
		return cmp.Or(cmp.Compare(y.TurnShare, x.TurnShare), cmp.Compare(x.Target, y.Target))
	})
	return out
}

// routingTargetNames maps the metric suffix of every target the arm's
// config names back to "provider/model".
func routingTargetNames(cfg *schemas.AgentConfig) map[string]string {
	out := map[string]string{}
	if cfg == nil {
		return out
	}
	provider := cmp.Or(cfg.Gateway.Provider, routingDefaultProvider)
	add := func(p, model string) {
		if model == "" {
			return
		}
		if _, rest, ok := strings.Cut(model, "/"); ok {
			model = rest
		}
		t := cmp.Or(p, provider) + "/" + model
		out[strings.Trim(routingSlugRe.ReplaceAllString(strings.ToLower(t), "_"), "_")] = t
	}
	add("", cfg.Model)
	if pol := cfg.Gateway.Routing; pol != nil {
		for _, t := range append(append([]schemas.RouteTarget{}, pol.Targets...), pol.Fallbacks...) {
			add(t.Provider, t.Model)
		}
	}
	return out
}
