package handlers

import (
	"slices"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// W3 customer support (docs/customer-support.md): the support scorecard of
// an experiment, from the frozen manifest and the sealed runs' metrics.json.

// Metric names the scorecard reads (bench/metrics/support.go and the
// built-ins); the package may not import metrics, so they are restated.
const (
	supportVerified   = "verified_success"
	supportCost       = "cost_usd"
	supportEscalated  = "escalated"
	supportEscCorrect = "escalation_correct"
	supportPolicy     = "policy_violation"
	supportUserTurns  = "user_turns"
	supportSimCost    = "simulator_cost_usd"
	supportTrack      = "support"
)

// SupportHandler serves the support scorecard.
type SupportHandler struct {
	mode  schemas.Mode
	index ReadIndex
}

// NewSupportHandler creates the support handler. index may be nil.
func NewSupportHandler(mode schemas.Mode, index ReadIndex) *SupportHandler {
	return &SupportHandler{mode: mode, index: index}
}

// RegisterRoutes registers GET /experiments/{id}/support.
func (h *SupportHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/support", lib.ChainMiddlewares(h.getSupport, middlewares...))
}

func (h *SupportHandler) getSupport(ctx *fasthttp.RequestCtx) {
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
	cond := string(ctx.QueryArgs().Peek("condition"))
	if cond == "" {
		cond = schemas.ConditionClean
	}
	out, err := ExperimentSupport(h.index, m, cond)
	if err != nil {
		sendDepError(ctx, h.mode, "support scorecard of experiment "+expID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// ExperimentSupport builds the scorecard over the experiment's sealed
// counted runs of condition.
func ExperimentSupport(index ReadIndex, m *schemas.Manifest, condition string) (*schemas.SupportSummary, error) {
	out := &schemas.SupportSummary{ExpID: m.ExpID, Suite: m.SuiteID + "@" + m.SuiteVersion, Condition: condition,
		SimulatorLabel: "scripted user", Synthetic: m.Experiment.NoPaidTraffic(), Arms: []schemas.SupportArm{}}
	if sim := m.Simulator; sim != nil {
		out.Support, out.SimulatorMode = true, sim.Mode
		if sim.Mode == schemas.SimulatorLLM {
			out.SimulatorLabel = "simulated user (model: " + sim.Provider + "/" + sim.Model + ")"
		}
	}
	byArm := map[string][]map[string]schemas.Value{}
	for _, rv := range countedRuns(index.Runs(m.ExpID)) {
		if !rv.Sealed || !rv.Slot.Counted || rv.Slot.Condition != condition {
			continue
		}
		metrics, err := readMetrics(index, rv.Run.RunID)
		if err != nil {
			return nil, err
		}
		if v, ok := metrics[supportEscalated]; ok && v.State != schemas.ValueNotApplicable {
			out.Support = true
		}
		byArm[rv.Slot.Arm] = append(byArm[rv.Slot.Arm], metrics)
	}
	if !out.Support {
		return out, nil
	}
	ids := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		out.Arms = append(out.Arms, supportArm(id, m.Arms[id].Label, byArm[id]))
	}
	return out, nil
}

// supportArm folds one arm's runs. It is pure.
func supportArm(id, label string, runs []map[string]schemas.Value) schemas.SupportArm {
	a := schemas.SupportArm{Arm: id, Label: label, Runs: len(runs)}
	cost, costKnown := 0.0, true
	for _, r := range runs {
		if v, ok := supportObserved(r, supportVerified); ok {
			a.Decided++
			if v >= 1 {
				a.Resolved++
			}
		}
		if v, ok := supportObserved(r, supportCost); ok {
			cost += v
		} else {
			costKnown = false
		}
	}
	if a.Decided > 0 {
		a.Resolution = supportPtr(float64(a.Resolved) / float64(a.Decided))
	}
	if costKnown && a.Resolved > 0 {
		a.CostPerResolution = supportPtr(cost / float64(a.Resolved))
	}
	a.EscalationRate = supportMean(runs, supportEscalated)
	a.EscalationCorrect = supportMean(runs, supportEscCorrect)
	a.PolicyViolationRate = supportMean(runs, supportPolicy)
	a.UserTurns = supportMean(runs, supportUserTurns)
	if m := supportMean(runs, supportSimCost); m != nil {
		n := 0
		for _, r := range runs {
			if _, ok := supportObserved(r, supportSimCost); ok {
				n++
			}
		}
		a.SimulatorCostUSD = supportPtr(*m * float64(n))
	}
	return a
}

func supportObserved(r map[string]schemas.Value, name string) (float64, bool) {
	v, ok := r[name]
	if !ok || v.State != schemas.ValueObserved || v.V == nil {
		return 0, false
	}
	return *v.V, true
}

func supportMean(runs []map[string]schemas.Value, name string) *float64 {
	sum, n := 0.0, 0
	for _, r := range runs {
		if v, ok := supportObserved(r, name); ok {
			sum += v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return supportPtr(sum / float64(n))
}

func supportPtr(v float64) *float64 { return &v }
