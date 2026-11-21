package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Improvement runs (bench/improve, docs/improvement-runs.md).

// ImproveSource reads and drives improvement runs; cmd/bifrost-bench
// implements it over the runs root.
type ImproveSource interface {
	List(ctx context.Context) (*improve.List, error)
	Get(ctx context.Context, id string) (*improve.Detail, error)
	Lineage(ctx context.Context, id string) (*improve.LineageView, error)
	// Create freezes a spec and starts it in the background.
	Create(ctx context.Context, spec *improve.Spec, actor string) (*improve.Summary, error)
	// Approve overrides a round's tripwires and continues in the background.
	Approve(ctx context.Context, id string, round int, actor, reason string) (*improve.Summary, error)
	Stop(ctx context.Context, id, actor string) (*improve.Summary, error)
}

// ImproveApproveRequest is the body of POST .../rounds/{k}/approve.
type ImproveApproveRequest struct {
	Reason string `json:"reason"`
}

// ImproveHandler serves /improvements.
type ImproveHandler struct {
	mode  schemas.Mode
	src   ImproveSource
	write lib.Middleware
}

// NewImproveHandler creates the handler. src may be nil; its routes then
// answer 503.
func NewImproveHandler(mode schemas.Mode, src ImproveSource, write lib.Middleware) *ImproveHandler {
	return &ImproveHandler{mode: mode, src: src, write: write}
}

// RegisterRoutes registers the improvement routes.
func (h *ImproveHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	r.GET(APIPrefix+"/improvements", lib.ChainMiddlewares(h.getList, middlewares...))
	r.POST(APIPrefix+"/improvements", lib.ChainMiddlewares(h.postCreate, writeMW...))
	r.GET(APIPrefix+"/improvements/{id}", lib.ChainMiddlewares(h.getOne, middlewares...))
	r.GET(APIPrefix+"/improvements/{id}/rounds/{k}", lib.ChainMiddlewares(h.getRound, middlewares...))
	r.GET(APIPrefix+"/improvements/{id}/lineage", lib.ChainMiddlewares(h.getLineage, middlewares...))
	r.POST(APIPrefix+"/improvements/{id}/rounds/{k}/approve", lib.ChainMiddlewares(h.postApprove, writeMW...))
	r.POST(APIPrefix+"/improvements/{id}/stop", lib.ChainMiddlewares(h.postStop, writeMW...))
}

func (h *ImproveHandler) ready(ctx *fasthttp.RequestCtx) bool {
	if h.src == nil {
		sendUnavailable(ctx, h.mode, "improvement runs")
		return false
	}
	return true
}

func (h *ImproveHandler) id(ctx *fasthttp.RequestCtx) (string, bool) {
	id := pathParam(ctx, "id")
	if !improve.ValidID(id) {
		sendNotFound(ctx, h.mode, "improvement", id)
		return "", false
	}
	return id, true
}

// sendErr maps the improve errors to statuses.
func (h *ImproveHandler) sendErr(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case errors.Is(err, improve.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": not found")
	case errors.Is(err, improve.ErrBusy), errors.Is(err, improve.ErrState):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": "+err.Error())
	case errors.Is(err, improve.ErrOffline):
		sendUnavailable(ctx, h.mode, "launching improvement runs")
	default:
		sendDepError(ctx, h.mode, what, err)
	}
}

// getList handles GET /api/bench/improvements.
func (h *ImproveHandler) getList(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	l, err := h.src.List(work)
	if err != nil {
		h.sendErr(ctx, "improvements", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, l)
}

// postCreate handles POST /api/bench/improvements: a spec as JSON or YAML,
// frozen and started in the background (202).
func (h *ImproveHandler) postCreate(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	spec, err := improve.ParseSpec(ctx.PostBody())
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	s, err := h.src.Create(work, spec, actorOf(ctx))
	if err != nil {
		h.sendErr(ctx, "improvement", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, s)
}

// getOne handles GET /api/bench/improvements/{id}.
func (h *ImproveHandler) getOne(ctx *fasthttp.RequestCtx) {
	id, ok := h.id(ctx)
	if !ok || !h.ready(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	d, err := h.src.Get(work, id)
	if err != nil {
		h.sendErr(ctx, "improvement "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, d)
}

// getRound handles GET /api/bench/improvements/{id}/rounds/{k}.
func (h *ImproveHandler) getRound(ctx *fasthttp.RequestCtx) {
	id, ok := h.id(ctx)
	if !ok || !h.ready(ctx) {
		return
	}
	k, err := strconv.Atoi(pathParam(ctx, "k"))
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	d, gerr := h.src.Get(work, id)
	if gerr != nil {
		h.sendErr(ctx, "improvement "+id, gerr)
		return
	}
	if err != nil || k < 1 || k > len(d.Rounds) {
		sendNotFound(ctx, h.mode, "round", pathParam(ctx, "k"))
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, d.Rounds[k-1])
}

// getLineage handles GET /api/bench/improvements/{id}/lineage.
func (h *ImproveHandler) getLineage(ctx *fasthttp.RequestCtx) {
	id, ok := h.id(ctx)
	if !ok || !h.ready(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	lv, err := h.src.Lineage(work, id)
	if err != nil {
		h.sendErr(ctx, "improvement "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, lv)
}

// postApprove handles POST /api/bench/improvements/{id}/rounds/{k}/approve.
func (h *ImproveHandler) postApprove(ctx *fasthttp.RequestCtx) {
	id, ok := h.id(ctx)
	if !ok || !h.ready(ctx) {
		return
	}
	k, err := strconv.Atoi(pathParam(ctx, "k"))
	if err != nil || k < 1 {
		sendNotFound(ctx, h.mode, "round", pathParam(ctx, "k"))
		return
	}
	var in ImproveApproveRequest
	if err := json.Unmarshal(ctx.PostBody(), &in); err != nil || in.Reason == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "body must be JSON {\"reason\": \"why the tripwire is overridden\"}")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	s, err := h.src.Approve(work, id, k, actorOf(ctx), in.Reason)
	if err != nil {
		h.sendErr(ctx, "improvement "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, s)
}

// postStop handles POST /api/bench/improvements/{id}/stop.
func (h *ImproveHandler) postStop(ctx *fasthttp.RequestCtx) {
	id, ok := h.id(ctx)
	if !ok || !h.ready(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	s, err := h.src.Stop(work, id, actorOf(ctx))
	if err != nil {
		h.sendErr(ctx, "improvement "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, s)
}
