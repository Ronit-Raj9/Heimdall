package handlers

import (
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
	"github.com/valyala/fasthttp"
)

// Round 6 N6: a comparison and the follow-ups its "Extend" action made, read
// together. Pooling happens here at read time, from the runs index and each
// stage's own analysis, so no sealed record is rewritten.

// ExtendPoolView is GET /experiments/{id}/extensions.
type ExtendPoolView struct {
	ExpID string `json:"exp_id"`
	// Parent is set on a follow-up: the comparison its runs count in.
	Parent string `json:"parent,omitempty"`
	// ParentPooled says whether this follow-up's runs are pooled there.
	ParentPooled bool `json:"parent_pooled"`
	// ParentReason says why not, when they are not.
	ParentReason string                `json:"parent_reason,omitempty"`
	Extensions   []verdict.PoolMember  `json:"extensions"`
	Pooled       *verdict.PooledResult `json:"pooled"` // null when no follow-up is pooled
}

// ExtendPoolHandler serves the pooled view.
type ExtendPoolHandler struct {
	mode     schemas.Mode
	index    ReadIndex
	analyzer Analyzer
}

// NewExtendPoolHandler creates the handler; index and analyzer may be nil.
func NewExtendPoolHandler(mode schemas.Mode, index ReadIndex, analyzer Analyzer) *ExtendPoolHandler {
	return &ExtendPoolHandler{mode: mode, index: index, analyzer: analyzer}
}

// RegisterRoutes registers GET /experiments/{id}/extensions.
func (h *ExtendPoolHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/extensions", lib.ChainMiddlewares(h.get, middlewares...))
}

func (h *ExtendPoolHandler) get(ctx *fasthttp.RequestCtx) {
	expID, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	if h.index == nil || h.analyzer == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	m, found := h.index.Manifest(expID)
	if !found {
		sendNotFound(ctx, h.mode, "experiment", expID)
		return
	}
	out := ExtendPoolView{ExpID: expID, Extensions: []verdict.PoolMember{}}
	parentID := expID
	if ref := m.Experiment.Extends; ref != nil && ref.ExpID != "" {
		parentID = ref.ExpID
		out.Parent = ref.ExpID
	}
	parent, found := h.index.Manifest(parentID)
	if !found {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
		return
	}
	var kids []*schemas.Manifest
	for _, id := range h.index.Experiments() {
		if k, ok := h.index.Manifest(id); ok && k.Experiment.Extends != nil && k.Experiment.Extends.ExpID == parentID {
			kids = append(kids, k)
		}
	}
	if len(kids) == 0 {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	stage := func(sm *schemas.Manifest) (verdict.PoolStage, error) {
		a, err := h.analyzer.Analyze(work, sm.ExpID, GradeSourceOriginal)
		if err != nil {
			return verdict.PoolStage{}, err
		}
		return verdict.PoolStage{Manifest: sm, Analysis: a, Runs: poolRunCount(h.index.Runs(sm.ExpID))}, nil
	}
	ps, err := stage(parent)
	if err != nil {
		sendDepError(ctx, h.mode, "analyze experiment "+parentID, err)
		return
	}
	stages := make([]verdict.PoolStage, 0, len(kids))
	for _, k := range kids {
		s, err := stage(k)
		if err != nil {
			sendDepError(ctx, h.mode, "analyze experiment "+k.ExpID, err)
			return
		}
		stages = append(stages, s)
	}
	pooled, members := verdict.PoolExtensions(ps, stages)
	if out.Parent != "" { // a follow-up: say whether its runs count in the parent
		for _, mem := range members {
			if mem.ExpID == expID {
				out.ParentPooled, out.ParentReason = mem.Pooled, mem.Reason
			}
		}
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
		return
	}
	out.Extensions, out.Pooled = members, pooled
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// poolRunCount is the number of sealed counted runs, superseded ones left out.
func poolRunCount(runs []RunView) int {
	n := 0
	for _, r := range runs {
		if r.Sealed && r.Slot.Counted && !r.Superseded {
			n++
		}
	}
	return n
}
