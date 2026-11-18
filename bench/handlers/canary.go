package handlers

import (
	"context"
	"errors"
	"slices"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// W5 drift canary (bench/canary, docs/drift-canary.md).

// CanarySource reads the drift canaries and starts a look;
// cmd/bifrost-bench implements it over canaries/*.yaml and the state folder.
type CanarySource interface {
	List(ctx context.Context) (*canary.List, error)
	Get(ctx context.Context, name string) (*canary.Detail, error)
	// Start begins one look (and its confirmation) in the background. A
	// canary already running answers an error wrapping canary.ErrBusy.
	Start(ctx context.Context, name string) (*canary.RunAccepted, error)
}

// CanaryHandler serves GET /canaries, GET /canaries/{name} and POST
// /canaries/{name}/run behind the write token.
type CanaryHandler struct {
	mode  schemas.Mode
	src   CanarySource
	write lib.Middleware
}

// NewCanaryHandler creates the handler. src may be nil; its routes then
// answer 503.
func NewCanaryHandler(mode schemas.Mode, src CanarySource, write lib.Middleware) *CanaryHandler {
	return &CanaryHandler{mode: mode, src: src, write: write}
}

// RegisterRoutes registers the canary routes.
func (h *CanaryHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	r.GET(APIPrefix+"/canaries", lib.ChainMiddlewares(h.getList, middlewares...))
	r.GET(APIPrefix+"/canaries/{name}", lib.ChainMiddlewares(h.getOne, middlewares...))
	r.POST(APIPrefix+"/canaries/{name}/run", lib.ChainMiddlewares(h.postRun, writeMW...))
}

// getList handles GET /api/bench/canaries.
func (h *CanaryHandler) getList(ctx *fasthttp.RequestCtx) {
	if h.src == nil {
		sendUnavailable(ctx, h.mode, "the drift canaries")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	l, err := h.src.List(work)
	if err != nil {
		sendDepError(ctx, h.mode, "canaries", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, l)
}

// getOne handles GET /api/bench/canaries/{name}.
func (h *CanaryHandler) getOne(ctx *fasthttp.RequestCtx) {
	if h.src == nil {
		sendUnavailable(ctx, h.mode, "the drift canaries")
		return
	}
	name, ok := h.name(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	d, err := h.src.Get(work, name)
	if err != nil {
		sendDepError(ctx, h.mode, "canary "+name, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, d)
}

// postRun handles POST /api/bench/canaries/{name}/run: 202 with the run
// started in the background, 409 while the canary is running.
func (h *CanaryHandler) postRun(ctx *fasthttp.RequestCtx) {
	if h.src == nil {
		sendUnavailable(ctx, h.mode, "the drift canaries")
		return
	}
	name, ok := h.name(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	res, err := h.src.Start(work, name)
	if errors.Is(err, canary.ErrOffline) {
		sendUnavailable(ctx, h.mode, "launching a canary look")
		return
	}
	if errors.Is(err, canary.ErrBusy) {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "canary "+name+": "+err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "canary "+name, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, res)
}

// name returns a valid canary name from the path, or answers 404.
func (h *CanaryHandler) name(ctx *fasthttp.RequestCtx) (string, bool) {
	name := pathParam(ctx, "name")
	if !canary.ValidName(name) {
		sendNotFound(ctx, h.mode, "canary", name)
		return "", false
	}
	return name, true
}
