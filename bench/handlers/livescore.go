package handlers

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livescore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// X6 live scoring (bench/livescore, docs/live-scoring.md).

// LivescoreSource is live scoring as the API serves it; cmd/bifrost-bench
// implements it over livescore/ in the project.
type LivescoreSource interface {
	Status(ctx context.Context) (*livescore.Status, error)
	Sessions(ctx context.Context, f livescore.SessionFilter) (*livescore.SessionList, error)
	// Compare returns nil, nil when no comparison has started.
	Compare(ctx context.Context) (*livescore.Comparison, error)
	Pull(ctx context.Context) (*livescore.PullResult, error)
}

// LivescoreHandler serves GET /livescore, GET /livescore/sessions, GET
// /livescore/compare and POST /livescore/pull behind the write token.
type LivescoreHandler struct {
	mode  schemas.Mode
	src   LivescoreSource
	write lib.Middleware
}

// NewLivescoreHandler creates the handler. src may be nil; its routes then
// answer 503.
func NewLivescoreHandler(mode schemas.Mode, src LivescoreSource, write lib.Middleware) *LivescoreHandler {
	return &LivescoreHandler{mode: mode, src: src, write: write}
}

// RegisterRoutes registers the live scoring routes.
func (h *LivescoreHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	r.GET(APIPrefix+"/livescore", lib.ChainMiddlewares(h.getStatus, middlewares...))
	r.GET(APIPrefix+"/livescore/sessions", lib.ChainMiddlewares(h.getSessions, middlewares...))
	r.GET(APIPrefix+"/livescore/compare", lib.ChainMiddlewares(h.getCompare, middlewares...))
	r.POST(APIPrefix+"/livescore/pull", lib.ChainMiddlewares(h.postPull, writeMW...))
}

func (h *LivescoreHandler) unavailable(ctx *fasthttp.RequestCtx) bool {
	if h.src == nil {
		sendUnavailable(ctx, h.mode, "live scoring")
		return true
	}
	return false
}

// getStatus handles GET /api/bench/livescore.
func (h *LivescoreHandler) getStatus(ctx *fasthttp.RequestCtx) {
	if h.unavailable(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	st, err := h.src.Status(work)
	if err != nil {
		sendDepError(ctx, h.mode, "live scoring", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, st)
}

// getSessions handles GET /api/bench/livescore/sessions?version=&flag=&limit=.
func (h *LivescoreHandler) getSessions(ctx *fasthttp.RequestCtx) {
	if h.unavailable(ctx) {
		return
	}
	q := ctx.QueryArgs()
	f := livescore.SessionFilter{Version: strings.TrimSpace(string(q.Peek("version"))), Flag: strings.TrimSpace(string(q.Peek("flag")))}
	if raw := string(q.Peek("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "limit must be 1 to 1000")
			return
		}
		f.Limit = n
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	l, err := h.src.Sessions(work, f)
	switch {
	case errors.Is(err, livescore.ErrBadFilter):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
	case errors.Is(err, livescore.ErrNotConfigured):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
	case err != nil:
		sendDepError(ctx, h.mode, "live sessions", err)
	default:
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, l)
	}
}

// getCompare handles GET /api/bench/livescore/compare: 404 until a
// comparison has started.
func (h *LivescoreHandler) getCompare(ctx *fasthttp.RequestCtx) {
	if h.unavailable(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	c, err := h.src.Compare(work)
	switch {
	case errors.Is(err, livescore.ErrNotConfigured):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
	case err != nil:
		sendDepError(ctx, h.mode, "live comparison", err)
	case c == nil:
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no live comparison yet: set compare: in livescore/config.yaml and pull")
	default:
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, c)
	}
}

// postPull handles POST /api/bench/livescore/pull: one pull now. It only
// reads the gateway; the write token guards the local files it changes.
func (h *LivescoreHandler) postPull(ctx *fasthttp.RequestCtx) {
	if h.unavailable(ctx) {
		return
	}
	if h.mode != schemas.ModeLive {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "a live pull needs a live server, not mode "+string(h.mode))
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	res, err := h.src.Pull(work)
	switch {
	case errors.Is(err, livescore.ErrNotConfigured):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
	case errors.Is(err, livescore.ErrOffline):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
	case err != nil && res == nil:
		sendDepError(ctx, h.mode, "live pull", err)
	case err != nil:
		lib.SendError(ctx, h.mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, "live pull: "+err.Error())
	default:
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, res)
	}
}
