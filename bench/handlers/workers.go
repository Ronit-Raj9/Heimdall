package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// workerCallTimeout bounds one worker API call; completing an upload
// extracts, verifies and seals a whole run folder.
const workerCallTimeout = 5 * time.Minute

// WorkerAPI is the remote worker coordinator (engine.Coordinator) as the
// worker routes use it. A nil WorkerAPI means worker mode is off.
type WorkerAPI interface {
	CheckToken(token string) bool
	Register(ctx context.Context, req *schemas.WorkerRegister) (*schemas.WorkerRegistered, error)
	Lease(ctx context.Context, workerID string) (*schemas.WorkerLease, error)
	Manifest(ctx context.Context, workerID, expID string) (*schemas.Manifest, error)
	Heartbeat(ctx context.Context, leaseID, workerID string) error
	Upload(ctx context.Context, leaseID, workerID string, offset int64, data []byte) (int64, error)
	Complete(ctx context.Context, leaseID string, req *schemas.WorkerComplete) (*schemas.WorkerResult, error)
	Release(ctx context.Context, leaseID string, req *schemas.WorkerRelease) error
	Workers() []schemas.WorkerInfo
}

// WorkersList is the answer of GET /api/bench/workers.
type WorkersList struct {
	Enabled bool                 `json:"enabled"` // worker mode is on (BENCH_WORKER_TOKEN set on this server)
	Workers []schemas.WorkerInfo `json:"workers"`
}

// WorkersHandler serves the remote worker API (docs/remote-execution.md).
// Every route except the read-only list requires the worker token as
// "Authorization: Bearer <token>"; the token is compared in constant time by
// the coordinator and never logged.
type WorkersHandler struct {
	mode schemas.Mode
	api  WorkerAPI
}

// NewWorkersHandler creates the worker handler. api may be nil: the list is
// then empty and every worker route answers 503.
func NewWorkersHandler(mode schemas.Mode, api WorkerAPI) *WorkersHandler {
	return &WorkersHandler{mode: mode, api: api}
}

// RegisterRoutes registers the worker routes under /api/bench/workers.
func (h *WorkersHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	p := APIPrefix + "/workers"
	r.GET(p, lib.ChainMiddlewares(h.list, middlewares...))
	r.POST(p+"/register", lib.ChainMiddlewares(h.auth(h.register), middlewares...))
	r.POST(p+"/lease", lib.ChainMiddlewares(h.auth(h.lease), middlewares...))
	r.GET(p+"/manifests/{exp_id}", lib.ChainMiddlewares(h.auth(h.manifest), middlewares...))
	r.POST(p+"/leases/{lease_id}/heartbeat", lib.ChainMiddlewares(h.auth(h.heartbeat), middlewares...))
	r.POST(p+"/leases/{lease_id}/upload", lib.ChainMiddlewares(h.auth(h.upload), middlewares...))
	r.POST(p+"/leases/{lease_id}/complete", lib.ChainMiddlewares(h.auth(h.complete), middlewares...))
	r.POST(p+"/leases/{lease_id}/release", lib.ChainMiddlewares(h.auth(h.release), middlewares...))
}

// list handles GET /api/bench/workers: every registered worker, for the UI.
// It is a read route like the rest of the API and carries no secret.
func (h *WorkersHandler) list(ctx *fasthttp.RequestCtx) {
	out := WorkersList{Workers: []schemas.WorkerInfo{}}
	if h.api != nil {
		out.Enabled = true
		out.Workers = h.api.Workers()
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// auth answers 503 when worker mode is off and 401 unless the request
// carries the worker token.
func (h *WorkersHandler) auth(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.api == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
				"worker API is off on this server: set BENCH_WORKER_TOKEN before starting serve")
			return
		}
		tok, ok := strings.CutPrefix(string(ctx.Request.Header.Peek("Authorization")), "Bearer ")
		if !ok || !h.api.CheckToken(strings.TrimSpace(tok)) {
			lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "worker token required: Authorization: Bearer <token>")
			return
		}
		next(ctx)
	}
}

func (h *WorkersHandler) register(ctx *fasthttp.RequestCtx) {
	var req schemas.WorkerRegister
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.api.Register(work, &req)
	if err != nil {
		h.sendErr(ctx, "register", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// lease answers 204 when there is no work for now.
func (h *WorkersHandler) lease(ctx *fasthttp.RequestCtx) {
	var req schemas.WorkerLeaseRequest
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := workContext(ctx, workerCallTimeout)
	defer cancel()
	out, err := h.api.Lease(work, req.WorkerID)
	if err != nil {
		h.sendErr(ctx, "lease", err)
		return
	}
	if out == nil {
		ctx.SetStatusCode(fasthttp.StatusNoContent)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *WorkersHandler) manifest(ctx *fasthttp.RequestCtx) {
	expID, ok := idParam(ctx, h.mode, "exp_id", "experiment")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	m, err := h.api.Manifest(work, string(ctx.QueryArgs().Peek("worker_id")), expID)
	if err != nil {
		h.sendErr(ctx, "manifest", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, m)
}

func (h *WorkersHandler) heartbeat(ctx *fasthttp.RequestCtx) {
	leaseID, ok := idParam(ctx, h.mode, "lease_id", "lease")
	if !ok {
		return
	}
	var req schemas.WorkerHeartbeat
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := h.api.Heartbeat(work, leaseID, req.WorkerID); err != nil {
		h.sendErr(ctx, "heartbeat", err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// upload takes one raw chunk; worker_id and offset are query arguments so
// the body stays the archive bytes.
func (h *WorkersHandler) upload(ctx *fasthttp.RequestCtx) {
	leaseID, ok := idParam(ctx, h.mode, "lease_id", "lease")
	if !ok {
		return
	}
	offset, err := strconv.ParseInt(string(ctx.QueryArgs().Peek("offset")), 10, 64)
	if err != nil || offset < 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "offset must be a non-negative integer")
		return
	}
	work, cancel := workContext(ctx, workerCallTimeout)
	defer cancel()
	n, err := h.api.Upload(work, leaseID, string(ctx.QueryArgs().Peek("worker_id")), offset, bytes.Clone(ctx.PostBody()))
	if err != nil {
		h.sendErr(ctx, "upload", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.WorkerUploadState{LeaseID: leaseID, Bytes: n})
}

func (h *WorkersHandler) complete(ctx *fasthttp.RequestCtx) {
	leaseID, ok := idParam(ctx, h.mode, "lease_id", "lease")
	if !ok {
		return
	}
	var req schemas.WorkerComplete
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := workContext(ctx, workerCallTimeout)
	defer cancel()
	out, err := h.api.Complete(work, leaseID, &req)
	if err != nil {
		h.sendErr(ctx, "complete", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *WorkersHandler) release(ctx *fasthttp.RequestCtx) {
	leaseID, ok := idParam(ctx, h.mode, "lease_id", "lease")
	if !ok {
		return
	}
	var req schemas.WorkerRelease
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := h.api.Release(work, leaseID, &req); err != nil {
		h.sendErr(ctx, "release", err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// decode reads a JSON body, refusing unknown fields.
func (h *WorkersHandler) decode(ctx *fasthttp.RequestCtx, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return false
	}
	return true
}

// sendErr maps the worker API's sentinel errors to statuses: 404 for an
// unknown worker or experiment, 410 for a lease that is gone (lost, ended or
// a late upload), 409 for an upload offset that does not match, 413 for an
// upload past the cap, 400 for a malformed request.
func (h *WorkersHandler) sendErr(ctx *fasthttp.RequestCtx, what string, err error) {
	msg := what + ": " + err.Error()
	switch {
	case errors.Is(err, schemas.ErrWorkerUnknown), errors.Is(err, schemas.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, msg)
	case errors.Is(err, schemas.ErrLeaseGone):
		lib.SendError(ctx, h.mode, fasthttp.StatusGone, lib.ErrTypeConflict, msg)
	case errors.Is(err, schemas.ErrUploadOffset):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, msg)
	case errors.Is(err, schemas.ErrUploadTooLarge):
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, msg)
	case errors.Is(err, schemas.ErrWorkerRequest):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
	default:
		sendDepError(ctx, h.mode, what, err)
	}
}
