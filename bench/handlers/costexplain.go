package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// CostExplainSource explains why one arm cost more than another and places
// an experiment's arms on the cost against success plane
// (docs/cost-explain.md). cmd/bifrost-bench implements it over the store's
// verified sealed runs and the price snapshot.
type CostExplainSource interface {
	// CostExplain splits B's cost minus A's. An unknown experiment wraps
	// schemas.ErrNotFound, a bad query costexplain.ErrBadQuery.
	CostExplain(ctx context.Context, expID, gradeSource string, q costexplain.Query) (*costexplain.Explanation, error)
	// Pareto places every arm of one experiment from its matrix rows.
	Pareto(ctx context.Context, expID, condition, x string) (*costexplain.Pareto, error)
}

// CostExplainHandler serves the cost explanation, the experiment's Pareto
// plane and the leaderboard's Pareto plane. The board's plane reads the run
// index directly, as GET /leaderboard does, so it needs no source.
type CostExplainHandler struct {
	mode   schemas.Mode
	source CostExplainSource
	index  ReadIndex
	now    func() time.Time

	// heldOutVisible serves the held-out board's plane (split=held_out), as
	// LeaderboardHandler does.
	heldOutVisible bool
}

// NewCostExplainHandler creates the handler; a nil source answers 503 on the
// experiment routes, a nil index on the board route.
func NewCostExplainHandler(mode schemas.Mode, source CostExplainSource, index ReadIndex) *CostExplainHandler {
	return &CostExplainHandler{mode: mode, source: source, index: index, now: time.Now}
}

// WithHeldOutVisible sets whether the held-out board's plane is served
// (server config held_out.visible).
func (h *CostExplainHandler) WithHeldOutVisible(visible bool) *CostExplainHandler {
	h.heldOutVisible = visible
	return h
}

// RegisterRoutes registers GET /experiments/{id}/cost-explain,
// GET /experiments/{id}/pareto and GET /leaderboard/pareto.
func (h *CostExplainHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/cost-explain", lib.ChainMiddlewares(h.getCostExplain, middlewares...))
	r.GET(APIPrefix+"/experiments/{id}/pareto", lib.ChainMiddlewares(h.getPareto, middlewares...))
	r.GET(APIPrefix+"/leaderboard/pareto", lib.ChainMiddlewares(h.getBoardPareto, middlewares...))
}

// getCostExplain handles GET /experiments/{id}/cost-explain with the query
// parameters a, b, condition and grade_source.
func (h *CostExplainHandler) getCostExplain(ctx *fasthttp.RequestCtx) {
	if h.source == nil {
		sendUnavailable(ctx, h.mode, "cost explain")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	args := ctx.QueryArgs()
	q := costexplain.Query{A: string(args.Peek("a")), B: string(args.Peek("b")), Condition: string(args.Peek("condition"))}
	gradeSource := string(args.Peek("grade_source"))
	if gradeSource == "" {
		gradeSource = schemas.GradeSourceOriginal
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	e, err := h.source.CostExplain(work, id, gradeSource, q)
	if errors.Is(err, costexplain.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "cost explain", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, e)
}

// getPareto handles GET /experiments/{id}/pareto with x and condition.
func (h *CostExplainHandler) getPareto(ctx *fasthttp.RequestCtx) {
	if h.source == nil {
		sendUnavailable(ctx, h.mode, "pareto")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	args := ctx.QueryArgs()
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	p, err := h.source.Pareto(work, id, string(args.Peek("condition")), string(args.Peek("x")))
	if errors.Is(err, costexplain.ErrBadQuery) || errors.Is(err, leaderboard.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "pareto", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, p)
}

// getBoardPareto handles GET /leaderboard/pareto: the board GET /leaderboard
// would answer for the same query (suite, condition, scope, synthetic,
// grade_source, split, k), each entry placed by its cost per verified
// success (x=cost_per_success, the default) or median cost per run.
func (h *CostExplainHandler) getBoardPareto(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	args := ctx.QueryArgs()
	if refuseHeldOutBoard(ctx, h.mode, h.heldOutVisible, string(args.Peek("split"))) {
		return
	}
	k, err := queryK(string(args.Peek("k")))
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	q := leaderboard.Query{
		Suite: string(args.Peek("suite")), Condition: string(args.Peek("condition")), Scope: string(args.Peek("scope")),
		Synthetic: string(args.Peek("synthetic")), GradeSource: string(args.Peek("grade_source")), Split: string(args.Peek("split")),
		K: k, Now: h.now(),
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	lb, err := leaderboard.Build(work, LeaderboardSource(h.index), q)
	if errors.Is(err, leaderboard.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "leaderboard", err)
		return
	}
	p, err := costexplain.ParetoFromLeaderboard(lb, string(args.Peek("x")))
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, p)
}
