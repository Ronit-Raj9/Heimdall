package handlers

import (
	"cmp"
	"slices"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// triageText is the label and description of a pattern when the catalog
// does not carry its metric.
type triageText struct {
	label, description string
}

// triagePatterns are the pattern metrics GET /experiments/{id}/triage
// reports, in display order. They are observed patterns, never root causes:
// the detectors are deterministic rules over the normalized events and say
// nothing about why a run failed (docs/triage.md).
var triagePatterns = []string{"pattern_loop", "pattern_unverified_finish", "pattern_ignored_error", "false_completion"}

// triageFallback labels each pattern when the catalog is absent or does not
// list it. The catalog's descriptor wins so the wording stays with the metric.
var triageFallback = map[string]triageText{
	"pattern_loop": {label: "Loop",
		description: "The same tool call repeated 3 or more times in a row, or 4 or more times with no edit between."},
	"pattern_unverified_finish": {label: "Unverified finish",
		description: "A success claim with no test command after the last edit."},
	"pattern_ignored_error": {label: "Ignored error",
		description: "A success claim after a failed tool result that none of the next 2 tool calls answered."},
	"false_completion": {label: "False completion",
		description: "A success claim while the hidden verifier failed."},
}

// TriageHandler serves the failure triage of an experiment: how often each
// observed pattern appears per arm, and in which runs.
type TriageHandler struct {
	mode    schemas.Mode
	index   ReadIndex
	catalog CatalogSource
}

// NewTriageHandler creates the triage handler. index and catalog may be nil;
// without the catalog the built-in labels are used.
func NewTriageHandler(mode schemas.Mode, index ReadIndex, catalog CatalogSource) *TriageHandler {
	return &TriageHandler{mode: mode, index: index, catalog: catalog}
}

// RegisterRoutes registers GET /experiments/{id}/triage.
func (h *TriageHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/triage", lib.ChainMiddlewares(h.getTriage, middlewares...))
}

// getTriage handles GET /api/bench/experiments/{id}/triage.
func (h *TriageHandler) getTriage(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	m, ok := h.index.Manifest(id)
	if !ok {
		sendNotFound(ctx, h.mode, "experiment", id)
		return
	}
	var catalog *schemas.Catalog
	if h.catalog != nil {
		catalog = h.catalog.Catalog()
	}
	out, err := BuildTriage(h.index, m, h.index.Runs(id), catalog)
	if err != nil {
		sendDepError(ctx, h.mode, "triage of experiment "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// BuildTriage groups the counted, sealed runs of each arm by pattern, from
// each run's metrics.json. Per arm, Num counts runs whose value is observed
// and positive, Den counts runs whose value is observed, and Unknown counts
// runs whose value is unknown or missing (sealed before the metric existed).
// A not_applicable value (false_completion on a passing run) counts nowhere:
// the pattern cannot occur there. Runs list the run ids showing the pattern,
// sorted. Arms with no runs still appear, with empty ratios.
func BuildTriage(index ReadIndex, m *schemas.Manifest, runs []RunView, catalog *schemas.Catalog) (*schemas.Triage, error) {
	arms := armIDs(m)
	views := countedSealed(runs, arms)
	metrics := make(map[string]map[string]schemas.Value, len(views))
	for _, rv := range views {
		vals, err := readMetrics(index, rv.Run.RunID)
		if err != nil {
			return nil, err
		}
		metrics[rv.Run.RunID] = vals
	}
	out := &schemas.Triage{ExpID: m.ExpID, Groups: make([]schemas.TriageGroup, 0, len(triagePatterns))}
	for _, name := range triagePatterns {
		label, desc := triageLabel(name, catalog)
		g := schemas.TriageGroup{Pattern: name, Label: label, Description: desc,
			PerArm: make(map[string]schemas.Ratio, len(arms)), Runs: make(map[string][]string, len(arms))}
		for _, arm := range arms {
			g.PerArm[arm] = schemas.Ratio{}
			g.Runs[arm] = []string{}
		}
		for _, rv := range views {
			arm, r := rv.Slot.Arm, g.PerArm[rv.Slot.Arm]
			v, found := metrics[rv.Run.RunID][name]
			switch {
			case !found || v.State == schemas.ValueUnknown || v.State == "":
				r.Unknown++
			case v.State == schemas.ValueObserved:
				r.Den++
				if f, ok := v.Float(); ok && f > 0 {
					r.Num++
					g.Runs[arm] = append(g.Runs[arm], rv.Run.RunID)
				}
			}
			g.PerArm[arm] = r
		}
		for _, arm := range arms {
			slices.Sort(g.Runs[arm])
		}
		out.Groups = append(out.Groups, g)
	}
	return out, nil
}

// countedSealed returns each slot's counted run when the slot is counted,
// the run is sealed and its arm is one of the experiment's, ordered by slot.
func countedSealed(runs []RunView, arms []string) []RunView {
	var views []RunView
	for _, rv := range countedRuns(runs) {
		if rv.Slot.Counted && rv.Sealed && slices.Contains(arms, rv.Slot.Arm) {
			views = append(views, rv)
		}
	}
	slices.SortFunc(views, func(x, y RunView) int {
		return cmp.Or(cmp.Compare(x.Slot.OrderIndex, y.Slot.OrderIndex), cmp.Compare(x.Slot.SlotID, y.Slot.SlotID))
	})
	return views
}

// triageLabel returns a pattern's label and description: the catalog's
// metric descriptor when listed, else the built-in fallback.
func triageLabel(name string, catalog *schemas.Catalog) (string, string) {
	if catalog != nil {
		for _, d := range catalog.Metrics {
			if d.Name == name && d.Label != "" {
				return d.Label, d.Description
			}
		}
	}
	fb := triageFallback[name]
	return fb.label, fb.description
}
