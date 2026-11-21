package handlers

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// WorkerTargets reads and sets where an organization's runs go
// (schemas.WorkerTargetView): the shared worker, a cloud sandbox platform or
// one of its own workers.
type WorkerTargets interface {
	Target(ctx context.Context, org string) (*schemas.WorkerTargetView, error)
	SetTarget(ctx context.Context, org, actor, target string) (*schemas.WorkerTargetView, error)
}

// ErrWorkerTarget marks a target the organization cannot use (not
// connected, not a known kind); it answers 400.
var ErrWorkerTarget = errors.New("invalid worker target")

// WorkerTargetHandler serves GET and PUT /api/bench/workers/target, the one
// "where runs go" setting of the Workers page. Members read it (the
// evaluate wizard lists its choices); admins set it.
type WorkerTargetHandler struct {
	mode  schemas.Mode
	api   WorkerTargets
	audit AuditFunc
}

// NewWorkerTargetHandler creates the handler; a nil api answers 503 (local
// mode, or a server without the keystore master key).
func NewWorkerTargetHandler(mode schemas.Mode, api WorkerTargets, audit AuditFunc) *WorkerTargetHandler {
	return &WorkerTargetHandler{mode: mode, api: api, audit: audit}
}

// RegisterRoutes registers the routes.
func (h *WorkerTargetHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/workers/target", lib.ChainMiddlewares(h.org(auth.RoleViewer, h.get), middlewares...))
	r.PUT(APIPrefix+"/workers/target", lib.ChainMiddlewares(h.org(auth.RoleAdmin, h.put), middlewares...))
}

const workerTargetOrgValue = "heimdall.workertarget.org"

func (h *WorkerTargetHandler) org(role auth.Role, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.api == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
				"choosing where runs go is part of the hosted platform and needs HEIMDALL_MASTER_KEY")
			return
		}
		p := auth.PrincipalFrom(ctx)
		switch {
		case !p.IsAuthenticated() || p.Kind == auth.KindRunner:
			lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in to see where runs go")
			return
		case p.OrgID == "" || !p.Can(role):
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "choosing where runs go needs the admin role")
			return
		}
		ctx.SetUserValue(workerTargetOrgValue, p.OrgID)
		ctx.Response.Header.Set("Cache-Control", "no-store")
		next(ctx)
	}
}

func (h *WorkerTargetHandler) get(ctx *fasthttp.RequestCtx) {
	org, _ := ctx.UserValue(workerTargetOrgValue).(string)
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	v, err := h.api.Target(work, org)
	if err != nil {
		sendDepError(ctx, h.mode, "where runs go", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, v)
}

func (h *WorkerTargetHandler) put(ctx *fasthttp.RequestCtx) {
	org, _ := ctx.UserValue(workerTargetOrgValue).(string)
	var req schemas.WorkerTargetSet
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body: want {\"target\": \"shared\" | \"platform:<name>\" | \"worker:<name>\"}")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	v, err := h.api.SetTarget(work, org, auth.PrincipalFrom(ctx).ActorID(), req.Target)
	switch {
	case errors.Is(err, ErrWorkerTarget):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	case err != nil:
		sendDepError(ctx, h.mode, "where runs go", err)
		return
	}
	if h.audit != nil {
		h.audit(ctx, org, "workers.target", "worker_target", v.Target, map[string]any{"target": v.Target})
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, v)
}
