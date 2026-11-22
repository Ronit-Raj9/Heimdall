package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// InboxSource is the failure inbox (bench/traceinbox, docs/failure-inbox.md);
// *traceinbox.Service implements it.
type InboxSource interface {
	List(ctx context.Context, state, source string) (*schemas.InboxList, error)
	Item(ctx context.Context, id string) (*schemas.InboxItemDetail, error)
	Accept(ctx context.Context, id string, req schemas.InboxAcceptRequest) (*schemas.InboxAcceptResult, error)
	BulkAccept(ctx context.Context, req schemas.InboxBulkAcceptRequest) (*schemas.InboxBulkAcceptResult, error)
	Reject(ctx context.Context, id string, req schemas.InboxRejectRequest) (*schemas.InboxActionResult, error)
	Snooze(ctx context.Context, id string, req schemas.InboxSnoozeRequest) (*schemas.InboxActionResult, error)
	Ingest(ctx context.Context, body []byte) (*schemas.InboxIngestResult, error)
	// Gate is the webhook's token, size and rate guard; nil when the
	// webhook is off.
	Gate() *traceinbox.Gate
}

// InboxHandler serves the failure inbox: the item list and detail for the
// UI, the accept, reject and snooze actions behind the write token, and the
// webhook ingest behind its own token.
type InboxHandler struct {
	mode  schemas.Mode
	inbox InboxSource
	write lib.Middleware
	now   func() time.Time
}

// NewInboxHandler creates the inbox handler. inbox may be nil; its routes
// then answer 503.
func NewInboxHandler(mode schemas.Mode, inbox InboxSource, write lib.Middleware) *InboxHandler {
	return &InboxHandler{mode: mode, inbox: inbox, write: write, now: time.Now}
}

// RegisterRoutes registers GET /inbox, GET /inbox/{id}, POST
// /inbox/{id}/accept|reject|snooze (write token) and POST /inbox/ingest
// (the webhook's own token, not the write token).
func (h *InboxHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	r.GET(APIPrefix+"/inbox", lib.ChainMiddlewares(h.getList, middlewares...))
	r.GET(APIPrefix+"/inbox/{id}", lib.ChainMiddlewares(h.getItem, middlewares...))
	r.POST(APIPrefix+"/inbox/{id}/accept", lib.ChainMiddlewares(h.postAccept, writeMW...))
	r.POST(APIPrefix+"/inbox/accept", lib.ChainMiddlewares(h.postBulkAccept, writeMW...))
	r.POST(APIPrefix+"/inbox/{id}/reject", lib.ChainMiddlewares(h.postReject, writeMW...))
	r.POST(APIPrefix+"/inbox/{id}/snooze", lib.ChainMiddlewares(h.postSnooze, writeMW...))
	r.POST(APIPrefix+"/inbox/ingest", lib.ChainMiddlewares(h.postIngest, middlewares...))
}

// getList handles GET /api/bench/inbox?state=&source=.
func (h *InboxHandler) getList(ctx *fasthttp.RequestCtx) {
	if h.inbox == nil {
		sendUnavailable(ctx, h.mode, "the failure inbox")
		return
	}
	state := strings.TrimSpace(string(ctx.QueryArgs().Peek("state")))
	if state != "" && !slices.Contains(traceinbox.States(), state) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "state must be one of "+strings.Join(traceinbox.States(), ", "))
		return
	}
	source := strings.TrimSpace(string(ctx.QueryArgs().Peek("source")))
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	l, err := h.inbox.List(work, state, source)
	if err != nil {
		h.sendErr(ctx, "inbox", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, l)
}

// getItem handles GET /api/bench/inbox/{id}.
func (h *InboxHandler) getItem(ctx *fasthttp.RequestCtx) {
	id, ok := h.itemID(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	d, err := h.inbox.Item(work, id)
	if err != nil {
		h.sendErr(ctx, "inbox item "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, d)
}

// postAccept handles POST /api/bench/inbox/{id}/accept: the item becomes a
// task draft that still needs a person (EDIT ME markers, task validate,
// task admit).
func (h *InboxHandler) postAccept(ctx *fasthttp.RequestCtx) {
	id, ok := h.itemID(ctx)
	if !ok {
		return
	}
	var req schemas.InboxAcceptRequest
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	res, err := h.inbox.Accept(work, id, req)
	if err != nil {
		h.sendErr(ctx, "accept "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, res)
}

// postBulkAccept handles POST /api/bench/inbox/accept: several items into
// one suite with one shared guide; each item is accepted or listed as
// failed with its reason.
func (h *InboxHandler) postBulkAccept(ctx *fasthttp.RequestCtx) {
	if h.inbox == nil {
		sendUnavailable(ctx, h.mode, "the failure inbox")
		return
	}
	if !h.liveOnly(ctx) {
		return
	}
	var req schemas.InboxBulkAcceptRequest
	if !h.decode(ctx, &req) {
		return
	}
	for _, id := range req.IDs {
		if !traceinbox.ValidItemID(id) {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "ids: "+id+" is not an inbox item id")
			return
		}
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	res, err := h.inbox.BulkAccept(work, req)
	if err != nil {
		h.sendErr(ctx, "bulk accept", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, res)
}

// postReject handles POST /api/bench/inbox/{id}/reject.
func (h *InboxHandler) postReject(ctx *fasthttp.RequestCtx) {
	id, ok := h.itemID(ctx)
	if !ok {
		return
	}
	var req schemas.InboxRejectRequest
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	res, err := h.inbox.Reject(work, id, req)
	if err != nil {
		h.sendErr(ctx, "reject "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, res)
}

// postSnooze handles POST /api/bench/inbox/{id}/snooze.
func (h *InboxHandler) postSnooze(ctx *fasthttp.RequestCtx) {
	id, ok := h.itemID(ctx)
	if !ok {
		return
	}
	var req schemas.InboxSnoozeRequest
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	res, err := h.inbox.Snooze(work, id, req)
	if err != nil {
		h.sendErr(ctx, "snooze "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, res)
}

// postIngest handles POST /api/bench/inbox/ingest: any tool pushes one
// flagged trace. It is guarded by the webhook's own token (compared by
// sha256 in constant time), a body cap and a rate limit, in that order, and
// answers 503 while no webhook source and token are configured.
func (h *InboxHandler) postIngest(ctx *fasthttp.RequestCtx) {
	if h.inbox == nil {
		sendUnavailable(ctx, h.mode, "the failure inbox")
		return
	}
	if !h.liveOnly(ctx) {
		return
	}
	gate := h.inbox.Gate()
	if gate == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, traceinbox.ErrOff.Error())
		return
	}
	if !gate.Check(string(ctx.Request.Header.Peek("Authorization"))) {
		ctx.Response.Header.Set("WWW-Authenticate", `Bearer realm="heimdall-inbox"`)
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "the inbox webhook needs its token: Authorization: Bearer <token>")
		return
	}
	if int64(len(ctx.PostBody())) > gate.MaxBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "push larger than the webhook's max_bytes; push one session")
		return
	}
	if !gate.Allow(h.now()) {
		ctx.Response.Header.Set("Retry-After", "60")
		lib.SendError(ctx, h.mode, fasthttp.StatusTooManyRequests, lib.ErrTypeUnavailable, "the inbox webhook's rate limit was reached; retry later")
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	res, err := h.inbox.Ingest(work, ctx.PostBody())
	if err != nil {
		h.sendErr(ctx, "ingest", err)
		return
	}
	status := fasthttp.StatusOK
	if res.Created {
		status = fasthttp.StatusCreated
	}
	lib.SendJSON(ctx, h.mode, status, res)
}

func (h *InboxHandler) itemID(ctx *fasthttp.RequestCtx) (string, bool) {
	if h.inbox == nil {
		sendUnavailable(ctx, h.mode, "the failure inbox")
		return "", false
	}
	if ctx.IsPost() && !h.liveOnly(ctx) {
		return "", false
	}
	id := pathParam(ctx, "id")
	if !traceinbox.ValidItemID(id) {
		sendNotFound(ctx, h.mode, "inbox item", id)
		return "", false
	}
	return id, true
}

// liveOnly refuses a write outside live mode with 403, as the review routes
// do: a recorded or synthetic server is a demo over fixtures, and the inbox
// writes task drafts into the project.
func (h *InboxHandler) liveOnly(ctx *fasthttp.RequestCtx) bool {
	if h.mode == schemas.ModeLive {
		return true
	}
	lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
		"inbox writes are refused in "+string(h.mode)+" mode; run the server in live mode")
	return false
}

// decode reads a JSON body strictly.
func (h *InboxHandler) decode(ctx *fasthttp.RequestCtx, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return false
	}
	return true
}

// sendErr maps the inbox's errors: unknown item 404, a state that forbids
// the action 409, a refused request 400, the webhook off 503.
func (h *InboxHandler) sendErr(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case errors.Is(err, traceinbox.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": "+err.Error())
	case errors.Is(err, traceinbox.ErrConflict):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": "+err.Error())
	case errors.Is(err, traceinbox.ErrInvalid):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, traceinbox.ErrOff):
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, err.Error())
	default:
		sendDepError(ctx, h.mode, what, err)
	}
}
