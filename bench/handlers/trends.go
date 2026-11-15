package handlers

import (
	"context"
	"errors"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/trends"
	"github.com/valyala/fasthttp"
)

// TrendSource computes trends from the derived results index
// (bench/resultsindex). The index is never read by a verdict, the
// leaderboard or a release decision, so this route can only inform a chart.
type TrendSource interface {
	Trends(ctx context.Context, q trends.Query) (*trends.Response, error)
}

// TrendsHandler serves time series per agent config lineage
// (docs/trends.md).
type TrendsHandler struct {
	mode schemas.Mode
	src  TrendSource
}

// NewTrendsHandler creates the trends handler. src may be nil; the route
// then answers 503.
func NewTrendsHandler(mode schemas.Mode, src TrendSource) *TrendsHandler {
	return &TrendsHandler{mode: mode, src: src}
}

// RegisterRoutes registers GET /trends.
func (h *TrendsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/trends", lib.ChainMiddlewares(h.getTrends, middlewares...))
}

// getTrends handles GET /api/bench/trends?suite=&agent=&metric=&condition=&synthetic=.
// Absent parameters take the defaults (every suite and agent, verified
// success, clean, live runs only); an unknown metric or synthetic value is a
// 400. Fewer than two points is an ordinary response the UI shows as empty.
func (h *TrendsHandler) getTrends(ctx *fasthttp.RequestCtx) {
	if h.src == nil {
		sendUnavailable(ctx, h.mode, "results index")
		return
	}
	args := ctx.QueryArgs()
	q := trends.Query{
		Suite:     string(args.Peek("suite")),
		Agent:     string(args.Peek("agent")),
		Metric:    string(args.Peek("metric")),
		Condition: string(args.Peek("condition")),
		Synthetic: string(args.Peek("synthetic")),
	}
	if _, err := q.Normalize(); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	resp, err := h.src.Trends(work, q)
	if errors.Is(err, trends.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "trends", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, resp)
}

// TrendsRouteDoc describes GET /trends for the OpenAPI document
// (handlers/openapi.go lists it in APIRoutes).
var TrendsRouteDoc = RouteDoc{
	Method: "GET", Path: APIPrefix + "/trends", Tag: "analysis",
	Summary:  "Time series per agent config lineage from the derived results index, with a sign-flip regression flag (docs/trends.md)",
	Response: trends.Response{},
	Query: []QueryDoc{
		{Name: "suite", Description: "Suite id@version; default every suite"},
		{Name: "agent", Description: "Agent config id; default every agent"},
		{Name: "metric", Description: "verified_success (default), cost_per_success, tokens, wall_time_s or false_completion"},
		{Name: "condition", Description: "Condition; default clean"},
		{Name: "synthetic", Description: "exclude (default), include or only"},
	},
}
