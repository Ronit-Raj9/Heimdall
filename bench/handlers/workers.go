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
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// workerCallTimeout bounds one worker API call; completing an upload
// extracts, verifies and seals a whole run folder.
const workerCallTimeout = 5 * time.Minute

// maxHeartbeatLogs bounds the log lines one heartbeat of a runner job lease
// may carry; older lines past it are dropped.
const maxHeartbeatLogs = 2000

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
	// Whoami answers GET /workers/whoami (Runners area).
	Whoami(ctx context.Context) (*schemas.RunnerWhoami, error)
}

// HostedWorkerAPI is a WorkerAPI that serves one hosted organization
// (engine.Coordinator with RemoteOptions.OrgID set). Its routes authenticate
// by runner token instead of the shared worker token (Runners area,
// docs/runners.md).
type HostedWorkerAPI interface {
	OrgID() string
}

// RunnerVerifier resolves a runner token (hmr_...) to its scope, or fails
// for an unknown, revoked or expired token. The runners package provides it.
type RunnerVerifier func(ctx context.Context, token string) (schemas.RunnerScope, error)

// runnerScopeValue is the request user value that carries a hosted runner's
// scope from auth to the coordinator call.
const runnerScopeValue = "heimdall.runners.scope"

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
	mode   schemas.Mode
	api    WorkerAPI
	orgID  string         // hosted: the organization the coordinator serves (Runners area)
	verify RunnerVerifier // hosted: checks a bearer runner token when the auth middleware set no runner principal
}

// NewWorkersHandler creates the worker handler. api may be nil: the list is
// then empty and every worker route answers 503.
func NewWorkersHandler(mode schemas.Mode, api WorkerAPI) *WorkersHandler {
	h := &WorkersHandler{mode: mode, api: api}
	if hosted, ok := api.(HostedWorkerAPI); ok && api != nil {
		h.orgID = hosted.OrgID()
	}
	return h
}

// WithRunnerVerifier makes a hosted handler check bearer runner tokens itself
// when the request reached it without a runner principal (Runners area).
func (h *WorkersHandler) WithRunnerVerifier(v RunnerVerifier) *WorkersHandler {
	h.verify = v
	return h
}

// RegisterRoutes registers the worker routes under /api/bench/workers.
func (h *WorkersHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	p := APIPrefix + "/workers"
	r.GET(p, lib.ChainMiddlewares(h.list, middlewares...))
	r.GET(p+"/whoami", lib.ChainMiddlewares(h.auth(h.whoami), middlewares...)) // Runners area: runner status
	r.POST(p+"/register", lib.ChainMiddlewares(h.auth(h.register), middlewares...))
	r.POST(p+"/lease", lib.ChainMiddlewares(h.auth(h.lease), middlewares...))
	r.GET(p+"/manifests/{exp_id}", lib.ChainMiddlewares(h.auth(h.manifest), middlewares...))
	r.POST(p+"/leases/{lease_id}/heartbeat", lib.ChainMiddlewares(h.auth(h.heartbeat), middlewares...))
	r.POST(p+"/leases/{lease_id}/upload", lib.ChainMiddlewares(h.auth(h.upload), middlewares...))
	r.POST(p+"/leases/{lease_id}/complete", lib.ChainMiddlewares(h.auth(h.complete), middlewares...))
	r.POST(p+"/leases/{lease_id}/release", lib.ChainMiddlewares(h.auth(h.release), middlewares...))
}

// list handles GET /api/bench/workers: every registered worker, for the UI.
// It is a read route like the rest of the API and carries no secret. On a
// hosted server only an admin of the organization sees runner
// infrastructure (host, build, labels, token record, GPUs); viewers and
// anonymous readers of a public organization get the availability summary.
func (h *WorkersHandler) list(ctx *fasthttp.RequestCtx) {
	out := WorkersList{Workers: []schemas.WorkerInfo{}}
	if h.api != nil {
		out.Enabled = true
		out.Workers = h.api.Workers()
		if h.orgID != "" && !h.seesInventory(ctx) {
			for i := range out.Workers {
				out.Workers[i] = publicWorker(out.Workers[i])
			}
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// seesInventory reports whether the caller may see the hosted runner
// inventory, which GET /runners already limits to admins.
func (h *WorkersHandler) seesInventory(ctx *fasthttp.RequestCtx) bool {
	p, ok := auth.Lookup(ctx)
	return ok && p.OrgID == h.orgID && p.Can(auth.RoleAdmin)
}

// publicWorker is the availability view of a worker: its name, state,
// capacity and pool, without infrastructure details.
func publicWorker(w schemas.WorkerInfo) schemas.WorkerInfo {
	return schemas.WorkerInfo{ID: w.ID, State: w.State, Slots: w.Slots, Running: w.Running,
		Completed: w.Completed, LastSeen: w.LastSeen, Pool: w.Pool}
}

// auth answers 503 when worker mode is off and 401 unless the request
// carries the worker token. A hosted handler (Runners area) takes a runner
// token instead: the runner principal the auth middleware set, or a bearer
// token its own verifier accepts; the token's scope (organization and pool)
// goes with every coordinator call, and a token of another organization is
// refused with 403.
func (h *WorkersHandler) auth(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.api == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
				"worker API is off on this server: set BENCH_WORKER_TOKEN before starting serve")
			return
		}
		if h.orgID != "" {
			h.runnerAuth(ctx, next)
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

// runnerAuth resolves a hosted request's runner scope (Runners area).
func (h *WorkersHandler) runnerAuth(ctx *fasthttp.RequestCtx, next fasthttp.RequestHandler) {
	var scope schemas.RunnerScope
	if p := auth.PrincipalFrom(ctx); p.Kind == auth.KindRunner {
		scope = schemas.RunnerScope{OrgID: p.OrgID, Pool: p.Pool, TokenID: p.TokenID}
	} else if tok, ok := strings.CutPrefix(string(ctx.Request.Header.Peek("Authorization")), "Bearer "); ok && h.verify != nil {
		work, cancel := workContext(ctx, readTimeout)
		s, err := h.verify(work, strings.TrimSpace(tok))
		cancel()
		if err == nil {
			scope = s
		}
	}
	if scope.TokenID == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized,
			"runner token required: Authorization: Bearer hmr_...")
		return
	}
	if scope.OrgID != h.orgID {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"the runner token belongs to another organization")
		return
	}
	ctx.SetUserValue(runnerScopeValue, scope)
	next(ctx)
}

// work is the context of one coordinator call: workContext plus, in hosted
// mode, the caller's runner scope.
func (h *WorkersHandler) work(ctx *fasthttp.RequestCtx, d time.Duration) (context.Context, context.CancelFunc) {
	work, cancel := workContext(ctx, d)
	if s, ok := ctx.UserValue(runnerScopeValue).(schemas.RunnerScope); ok {
		work = schemas.WithRunnerScope(work, s)
	}
	return work, cancel
}

// whoami handles GET /api/bench/workers/whoami: the caller's token scope and
// its registered runners, for `bifrost-bench runner status`.
func (h *WorkersHandler) whoami(ctx *fasthttp.RequestCtx) {
	work, cancel := h.work(ctx, readTimeout)
	defer cancel()
	out, err := h.api.Whoami(work)
	if err != nil {
		h.sendErr(ctx, "whoami", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *WorkersHandler) register(ctx *fasthttp.RequestCtx) {
	var req schemas.WorkerRegister
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := h.work(ctx, readTimeout)
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
	work, cancel := h.work(ctx, workerCallTimeout)
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
	work, cancel := h.work(ctx, readTimeout)
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
	if len(req.Logs) > maxHeartbeatLogs {
		req.Logs = req.Logs[len(req.Logs)-maxHeartbeatLogs:]
	}
	if len(req.Events) > schemas.MaxHeartbeatEvents { // live events of a slot lease: the newest only
		req.Events = req.Events[len(req.Events)-schemas.MaxHeartbeatEvents:]
	}
	work, cancel := h.work(ctx, readTimeout)
	defer cancel()
	work = schemas.WithHeartbeatLogs(work, req.Logs) // runner jobs (gap G05): log lines of a job lease
	work = schemas.WithHeartbeatEvents(work, req.Events)
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
	work, cancel := h.work(ctx, workerCallTimeout)
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
	work, cancel := h.work(ctx, workerCallTimeout)
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
	work, cancel := h.work(ctx, readTimeout)
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
	case errors.Is(err, schemas.ErrWorkerAuth): // Runners area
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, msg)
	case errors.Is(err, schemas.ErrWorkerScope): // Runners area: another organization, pool or token
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, msg)
	default:
		sendDepError(ctx, h.mode, what, err)
	}
}
