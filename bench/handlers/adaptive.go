package handlers

import (
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// W7 adaptive trials and suite tiers (docs/adaptive-trials.md,
// docs/suite-tiers.md): the "Trials and tier" panel of an experiment. The
// frozen plan comes from the manifest; trials used, runs saved, the stopping
// reason and the "buy more evidence" line from the verdict, which replays
// the frozen rule over the sealed runs.

// AdaptiveView is GET /experiments/{id}/adaptive.
type AdaptiveView struct {
	ExpID        string                  `json:"exp_id"`
	Tier         string                  `json:"tier"`       // "" when the experiment named none
	TierTasks    []string                `json:"tier_tasks"` // the tier's tasks at plan time
	Trials       *schemas.TrialsPolicy   `json:"trials"`     // as declared; null for the classic fixed design
	Plan         *schemas.AdaptivePlan   `json:"plan"`       // the frozen adaptive design; null when fixed
	Report       *schemas.AdaptiveReport `json:"report"`     // null when fixed
	MoreEvidence *schemas.MoreEvidence   `json:"more_evidence"`
	Extends      *schemas.ExtendsRef     `json:"extends"`
	Decision     schemas.Decision        `json:"decision"`
	MaxSlots     int                     `json:"max_slots"` // counted slots planned
}

// AdaptiveHandler serves the trials and tier panel.
type AdaptiveHandler struct {
	mode     schemas.Mode
	index    ReadIndex
	analyzer Analyzer
}

// NewAdaptiveHandler creates the handler; index and analyzer may be nil.
func NewAdaptiveHandler(mode schemas.Mode, index ReadIndex, analyzer Analyzer) *AdaptiveHandler {
	return &AdaptiveHandler{mode: mode, index: index, analyzer: analyzer}
}

// RegisterRoutes registers GET /experiments/{id}/adaptive.
func (h *AdaptiveHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/adaptive", lib.ChainMiddlewares(h.getAdaptive, middlewares...))
}

func (h *AdaptiveHandler) getAdaptive(ctx *fasthttp.RequestCtx) {
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
	if h.analyzer == nil {
		sendUnavailable(ctx, h.mode, "analyzer")
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	a, err := h.analyzer.Analyze(work, expID, GradeSourceOriginal)
	if err != nil {
		sendDepError(ctx, h.mode, "analyze experiment "+expID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AdaptiveOf(m, a))
}

// AdaptiveOf builds the view from a manifest and its analysis (nil when
// there is none yet).
func AdaptiveOf(m *schemas.Manifest, a *schemas.Analysis) AdaptiveView {
	out := AdaptiveView{ExpID: m.ExpID, Tier: m.Experiment.Tier, TierTasks: []string{}, Trials: m.Experiment.Trials,
		Plan: m.Adaptive, Extends: m.Experiment.Extends}
	if m.Tier != nil {
		out.Tier, out.TierTasks = m.Tier.Name, append([]string{}, m.Tier.Tasks...)
	}
	for _, sl := range m.Slots {
		if sl.Counted {
			out.MaxSlots++
		}
	}
	if a != nil {
		out.Report = a.Verdict.Adaptive
		out.MoreEvidence = a.Verdict.MoreEvidence
		out.Decision = a.Verdict.Decision
	}
	return out
}
