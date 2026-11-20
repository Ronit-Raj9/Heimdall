package handlers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// ErrPlanRefused marks a ForecastSource error that is the planner refusing
// the experiment. The estimate route answers it as part of the forecast
// (HTTP 200, fit unknown, the reason in notes), as the plan route does.
var ErrPlanRefused = errors.New("plan refused")

// ForecastSource forecasts experiments and builds model comparison matrices
// (docs/forecast.md, docs/model-matrix.md). cmd/bifrost-bench implements it
// over the planner, the price snapshot and the store's verified sealed-run
// index.
type ForecastSource interface {
	// Estimate plans exp and forecasts it; nothing is frozen. A planner
	// refusal wraps ErrPlanRefused.
	Estimate(ctx context.Context, exp *schemas.Experiment) (*schemas.Forecast, error)
	// Matrix compares every arm of one experiment. An unknown experiment
	// wraps schemas.ErrNotFound, a bad query leaderboard.ErrBadQuery.
	Matrix(ctx context.Context, expID string, q leaderboard.MatrixQuery) (*schemas.ModelMatrix, error)
}

// ForecastHandler serves the cost and time forecast of a planned experiment
// and the model comparison matrix of a sealed one.
type ForecastHandler struct {
	mode   schemas.Mode
	source ForecastSource
	write  lib.Middleware
	now    func() time.Time
}

// NewForecastHandler creates the forecast handler. write guards the estimate
// route the way it guards plan, since both run the planner; source may be
// nil, and then both routes answer 503.
func NewForecastHandler(mode schemas.Mode, source ForecastSource, write lib.Middleware) *ForecastHandler {
	return &ForecastHandler{mode: mode, source: source, write: write, now: time.Now}
}

// RegisterRoutes registers POST /api/bench/estimate and
// GET /api/bench/experiments/{id}/matrix.
func (h *ForecastHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := slices.Clone(middlewares)
	if h.write != nil {
		writeMW = append(writeMW, h.write)
	}
	r.POST(APIPrefix+"/estimate", lib.ChainMiddlewares(h.postEstimate, writeMW...))
	r.GET(APIPrefix+"/experiments/{id}/matrix", lib.ChainMiddlewares(h.getMatrix, middlewares...))
}

// postEstimate handles POST /api/bench/estimate. The body is the experiment,
// as for POST /experiments/plan. A planner refusal is part of the answer.
func (h *ForecastHandler) postEstimate(ctx *fasthttp.RequestCtx) {
	if h.source == nil {
		sendUnavailable(ctx, h.mode, "forecast")
		return
	}
	exp, status, err := DecodeExperiment(mediaType(ctx), ctx.PostBody())
	if err != nil {
		errType := lib.ErrTypeBadRequest
		if status == fasthttp.StatusUnsupportedMediaType {
			errType = lib.ErrTypeUnsupportedMedia
		}
		lib.SendError(ctx, h.mode, status, errType, err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	f, err := h.source.Estimate(work, exp)
	switch {
	case errors.Is(err, ErrPlanRefused):
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, RefusedForecast(exp, err, h.now()))
	case err != nil:
		sendDepError(ctx, h.mode, "estimate", err)
	default:
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, f)
	}
}

// getMatrix handles GET /api/bench/experiments/{id}/matrix with the query
// parameters condition, grade_source, k and alpha.
func (h *ForecastHandler) getMatrix(ctx *fasthttp.RequestCtx) {
	if h.source == nil {
		sendUnavailable(ctx, h.mode, "matrix")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	args := ctx.QueryArgs()
	k, err := queryK(string(args.Peek("k")))
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	alpha := 0.0
	if raw := strings.TrimSpace(string(args.Peek("alpha"))); raw != "" {
		if alpha, err = strconv.ParseFloat(raw, 64); err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("alpha %q is not a number", raw))
			return
		}
	}
	q := leaderboard.MatrixQuery{Condition: string(args.Peek("condition")), GradeSource: string(args.Peek("grade_source")), K: k, Alpha: alpha, Split: string(args.Peek("split"))}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	mx, err := h.source.Matrix(work, id, q)
	if errors.Is(err, leaderboard.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "matrix", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, mx)
}

// RefusedForecast is the forecast answered when the planner refuses the
// experiment: nothing estimated, fit unknown, the planner's error in notes.
// It is pure.
func RefusedForecast(exp *schemas.Experiment, err error, now time.Time) *schemas.Forecast {
	unknown := schemas.ForecastRange{Kind: schemas.RangeUnknown, Reason: "the plan was refused"}
	return &schemas.Forecast{
		ExpID: exp.ID, Suite: exp.Suite, GeneratedAt: now.UTC(), Arms: []schemas.ArmForecast{},
		CostUSD: unknown, AgentTimeS: unknown, WallClockS: unknown, Concurrency: max(1, exp.Concurrency),
		BudgetUSD: exp.Budget.TotalUSD, Fit: schemas.FitUnknown, FitReason: "the plan was refused",
		Basis: "No forecast: the experiment could not be planned.", Notes: []string{"plan experiment: " + err.Error()},
	}
}
