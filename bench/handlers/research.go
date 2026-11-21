package handlers

import (
	"slices"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// W4 research with Tavily (docs/research-tasks.md): the research scorecard
// of an experiment, from the frozen manifest and the sealed runs'
// metrics.json.

// Metric names the scorecard reads (bench/metrics/research.go and the
// built-ins); the package may not import metrics, so they are restated.
const (
	researchVerified  = "verified_success"
	researchSearches  = "searches"
	researchCited     = "sources_cited"
	researchGrounded  = "citations_grounded"
	researchPrecision = "citation_precision"
	researchUnmatched = "external_unmatched"
)

// ResearchHandler serves the research scorecard.
type ResearchHandler struct {
	mode  schemas.Mode
	index ReadIndex
}

// NewResearchHandler creates the research handler. index may be nil.
func NewResearchHandler(mode schemas.Mode, index ReadIndex) *ResearchHandler {
	return &ResearchHandler{mode: mode, index: index}
}

// RegisterRoutes registers GET /experiments/{id}/research.
func (h *ResearchHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/research", lib.ChainMiddlewares(h.getResearch, middlewares...))
}

func (h *ResearchHandler) getResearch(ctx *fasthttp.RequestCtx) {
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
	out, err := ExperimentResearch(h.index, m, cond)
	if err != nil {
		sendDepError(ctx, h.mode, "research scorecard of experiment "+expID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// ExperimentResearch builds the scorecard over the experiment's sealed
// counted runs of condition. It applies when any such run reported searches
// (a value other than not applicable).
func ExperimentResearch(index ReadIndex, m *schemas.Manifest, condition string) (*schemas.ResearchSummary, error) {
	ext := m.Experiment.External
	out := &schemas.ResearchSummary{ExpID: m.ExpID, Suite: m.SuiteID + "@" + m.SuiteVersion, Condition: condition,
		Replay: ext == "" || ext == schemas.ExternalReplay, Synthetic: m.Experiment.NoPaidTraffic(), Arms: []schemas.ResearchArm{}}
	byArm := map[string][]map[string]schemas.Value{}
	for _, rv := range countedRuns(index.Runs(m.ExpID)) {
		if !rv.Sealed || !rv.Slot.Counted || rv.Slot.Condition != condition {
			continue
		}
		metrics, err := readMetrics(index, rv.Run.RunID)
		if err != nil {
			return nil, err
		}
		if v, ok := metrics[researchSearches]; ok && v.State != schemas.ValueNotApplicable {
			out.Research = true
		}
		byArm[rv.Slot.Arm] = append(byArm[rv.Slot.Arm], metrics)
	}
	if !out.Research {
		return out, nil
	}
	ids := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		runs := byArm[id]
		a := schemas.ResearchArm{Arm: id, Label: m.Arms[id].Label, Runs: len(runs)}
		for _, r := range runs {
			if v, ok := supportObserved(r, researchVerified); ok {
				a.Decided++
				if v >= 1 {
					a.Passed++
				}
			}
		}
		if a.Decided > 0 {
			a.Verified = supportPtr(float64(a.Passed) / float64(a.Decided))
		}
		a.Searches = supportMean(runs, researchSearches)
		a.SourcesCited = supportMean(runs, researchCited)
		a.CitationsGrounded = supportMean(runs, researchGrounded)
		a.CitationPrecision = supportMean(runs, researchPrecision)
		a.Unmatched = supportMean(runs, researchUnmatched)
		out.Arms = append(out.Arms, a)
	}
	return out, nil
}
