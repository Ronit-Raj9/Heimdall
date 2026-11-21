package handlers

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// WorkerConnAdmin is the bring-your-own-worker area (package byoworker) as
// the routes use it. Every call is scoped to one organization.
type WorkerConnAdmin interface {
	List(ctx context.Context, orgID string) (*schemas.WorkerConnectionsList, error)
	Get(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error)
	Create(ctx context.Context, orgID, actor string, in schemas.WorkerConnectionCreate) (*schemas.WorkerConnectionCreated, error)
	Check(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error)
	Start(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error)
	Stop(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error)
	Delete(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error)
	InstallCommand(ctx context.Context, orgID, id string) (string, error)
	SetDefaultWorker(ctx context.Context, orgID, actor, pool string) (string, error)
	KubeManifest(namespace string) string
}

// WorkerConnErrors classifies the area's errors: invalid answers 400,
// notFound 404, refused (the platform refused the credential) 400.
type WorkerConnErrors struct {
	Invalid, NotFound, Refused error
}

// WorkerConnsHandler serves /api/bench/workers/connections and
// /api/bench/workers/default (hosted only). Reading needs a member of the
// org; every write needs an admin, since a worker spends the org's money on
// its own cloud account.
type WorkerConnsHandler struct {
	mode  schemas.Mode
	api   WorkerConnAdmin
	errs  WorkerConnErrors
	audit AuditFunc
}

// NewWorkerConnsHandler creates the handler; a nil api answers 503.
func NewWorkerConnsHandler(mode schemas.Mode, api WorkerConnAdmin, errs WorkerConnErrors, audit AuditFunc) *WorkerConnsHandler {
	return &WorkerConnsHandler{mode: mode, api: api, errs: errs, audit: audit}
}

// RegisterRoutes registers the routes.
func (h *WorkerConnsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	p := APIPrefix + "/workers/connections"
	r.GET(p, lib.ChainMiddlewares(h.org(auth.RoleViewer, h.list), middlewares...))
	r.POST(p, lib.ChainMiddlewares(h.org(auth.RoleAdmin, h.create), middlewares...))
	r.GET(APIPrefix+"/workers/kubernetes-manifest", lib.ChainMiddlewares(h.org(auth.RoleViewer, h.manifest), middlewares...))
	r.PUT(APIPrefix+"/workers/default", lib.ChainMiddlewares(h.org(auth.RoleAdmin, h.setDefault), middlewares...))
	r.GET(p+"/{id}", lib.ChainMiddlewares(h.org(auth.RoleViewer, h.get), middlewares...))
	r.POST(p+"/{id}/check", lib.ChainMiddlewares(h.org(auth.RoleAdmin, h.action("check")), middlewares...))
	r.POST(p+"/{id}/start", lib.ChainMiddlewares(h.org(auth.RoleAdmin, h.action("start")), middlewares...))
	r.POST(p+"/{id}/stop", lib.ChainMiddlewares(h.org(auth.RoleAdmin, h.action("stop")), middlewares...))
	r.POST(p+"/{id}/remove", lib.ChainMiddlewares(h.org(auth.RoleAdmin, h.action("remove")), middlewares...))
	r.POST(p+"/{id}/install-command", lib.ChainMiddlewares(h.org(auth.RoleAdmin, h.installCommand), middlewares...))
}

const workerConnsOrgValue = "heimdall.workerconns.org"

func (h *WorkerConnsHandler) org(role auth.Role, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.api == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
				"bring your own worker is part of the hosted platform and needs HEIMDALL_MASTER_KEY")
			return
		}
		p := auth.PrincipalFrom(ctx)
		switch {
		case !p.IsAuthenticated() || p.Kind == auth.KindRunner:
			lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in to manage workers")
			return
		case p.OrgID == "" || !p.Can(role):
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "connecting and starting workers needs the admin role")
			return
		}
		ctx.SetUserValue(workerConnsOrgValue, p.OrgID)
		next(ctx)
	}
}

func workerConnOrg(ctx *fasthttp.RequestCtx) string {
	s, _ := ctx.UserValue(workerConnsOrgValue).(string)
	return s
}

func (h *WorkerConnsHandler) list(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.api.List(work, workerConnOrg(ctx))
	if err != nil {
		h.sendErr(ctx, "list workers", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *WorkerConnsHandler) get(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.mode, "id", "worker")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.api.Get(work, workerConnOrg(ctx), id)
	if err != nil {
		h.sendErr(ctx, "worker", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// create answers the connection; a VM's install command (it holds the
// runner token) is shown once and never audited.
func (h *WorkerConnsHandler) create(ctx *fasthttp.RequestCtx) {
	var req schemas.WorkerConnectionCreate
	if err := decodeJSONStrict(bytes.TrimSpace(ctx.PostBody()), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, workerPlatformTimeout)
	defer cancel()
	org := workerConnOrg(ctx)
	out, err := h.api.Create(work, org, auth.PrincipalFrom(ctx).ActorID(), req)
	if err != nil {
		h.sendErr(ctx, "connect worker", err)
		return
	}
	h.write(ctx, org, "worker.connect", out.Connection.ID, map[string]any{"name": out.Connection.Name, "platform": out.Connection.Platform})
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, out)
}

func (h *WorkerConnsHandler) action(kind string) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		id, ok := idParam(ctx, h.mode, "id", "worker")
		if !ok {
			return
		}
		// Starting a box or checking a platform waits on the platform's API.
		work, cancel := workContext(ctx, workerPlatformTimeout)
		defer cancel()
		org := workerConnOrg(ctx)
		var out *schemas.WorkerConnection
		var err error
		switch kind {
		case "check":
			out, err = h.api.Check(work, org, id)
		case "start":
			out, err = h.api.Start(work, org, id)
		case "stop":
			out, err = h.api.Stop(work, org, id)
		case "remove":
			out, err = h.api.Delete(work, org, id)
		}
		if err != nil {
			h.sendErr(ctx, kind+" worker", err)
			return
		}
		if kind != "check" {
			h.write(ctx, org, "worker."+kind, id, map[string]any{"name": out.Name, "platform": out.Platform})
		}
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
	}
}

func (h *WorkerConnsHandler) installCommand(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.mode, "id", "worker")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	org := workerConnOrg(ctx)
	cmd, err := h.api.InstallCommand(work, org, id)
	if err != nil {
		h.sendErr(ctx, "install command", err)
		return
	}
	h.write(ctx, org, "worker.install_command", id, nil)
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.WorkerInstallCommand{Install: cmd})
}

func (h *WorkerConnsHandler) setDefault(ctx *fasthttp.RequestCtx) {
	var req schemas.WorkerDefault
	if err := decodeJSONStrict(bytes.TrimSpace(ctx.PostBody()), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	org := workerConnOrg(ctx)
	pool, err := h.api.SetDefaultWorker(work, org, auth.PrincipalFrom(ctx).ActorID(), req.Worker)
	if err != nil {
		h.sendErr(ctx, "default worker", err)
		return
	}
	h.write(ctx, org, "worker.default", pool, map[string]any{"worker": pool})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.WorkerDefault{Worker: pool})
}

// manifest serves the RBAC an org applies before connecting a cluster.
func (h *WorkerConnsHandler) manifest(ctx *fasthttp.RequestCtx) {
	ns := strings.TrimSpace(string(ctx.QueryArgs().Peek("namespace")))
	if ns == "" {
		ns = "heimdall"
	}
	if !schemas.ValidPoolName(ns) || strings.ToLower(ns) != ns || strings.ContainsAny(ns, "._") {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "namespace must be lowercase letters, digits and '-'")
		return
	}
	ctx.SetContentType("application/yaml; charset=utf-8")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyString(h.api.KubeManifest(ns))
}

func (h *WorkerConnsHandler) write(ctx *fasthttp.RequestCtx, org, action, targetID string, details map[string]any) {
	if h.audit != nil {
		h.audit(ctx, org, action, "worker", targetID, details)
	}
}

func (h *WorkerConnsHandler) sendErr(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case h.errs.Invalid != nil && errors.Is(err, h.errs.Invalid), h.errs.Refused != nil && errors.Is(err, h.errs.Refused):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case h.errs.NotFound != nil && errors.Is(err, h.errs.NotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": "+err.Error())
	default:
		sendDepError(ctx, h.mode, what, err)
	}
}

// workerPlatformTimeout bounds a call that waits on a worker platform's API
// (two or three HTTPS calls of at most 30 s each).
const workerPlatformTimeout = 90 * time.Second
