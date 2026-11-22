package handlers

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/openmodel"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// OpenModels is the open model service (openmodel.Service) as the routes
// use it.
type OpenModels interface {
	Catalog() openmodel.CatalogView
	List(ctx context.Context, org string) ([]openmodel.Deployment, error)
	Get(ctx context.Context, org, id string) (*openmodel.Deployment, error)
	Start(ctx context.Context, org, actor string, req openmodel.StartRequest) (*openmodel.Deployment, error)
	Stop(ctx context.Context, org, id, actor string) (*openmodel.Deployment, error)
	OrphanApps(ctx context.Context, org string) ([]openmodel.AppInfo, error)
}

// OpenModelsHandler serves Models, Open models under /api/bench/open-models
// (lane Z05): serve an open model (Qwen, DeepSeek, Llama) with vLLM or
// SGLang on the org's own Modal account, registered as the org's provider
// key while it runs. Reads need a viewer of the org; starting and stopping
// spend the org's Modal credit and need an admin.
type OpenModelsHandler struct {
	mode  schemas.Mode
	api   OpenModels
	audit AuditFunc
}

// NewOpenModelsHandler creates the handler; a nil api answers 503 (local
// mode, or a server without a database or keystore master key).
func NewOpenModelsHandler(mode schemas.Mode, api OpenModels, audit AuditFunc) *OpenModelsHandler {
	return &OpenModelsHandler{mode: mode, api: api, audit: audit}
}

// OpenModelsList is GET /open-models.
type OpenModelsList struct {
	Deployments []openmodel.Deployment `json:"deployments"`
}

// OpenModelApps is GET /open-models/modal-apps: serving apps on the org's
// Modal account that no running deployment owns.
type OpenModelApps struct {
	Apps []openmodel.AppInfo `json:"apps"`
}

// RegisterRoutes registers the routes.
func (h *OpenModelsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	p := APIPrefix + "/open-models"
	r.GET(p+"/catalog", lib.ChainMiddlewares(h.role(auth.RoleViewer, h.catalog), middlewares...))
	r.GET(p+"/modal-apps", lib.ChainMiddlewares(h.role(auth.RoleAdmin, h.apps), middlewares...))
	r.GET(p, lib.ChainMiddlewares(h.role(auth.RoleViewer, h.list), middlewares...))
	r.POST(p, lib.ChainMiddlewares(h.role(auth.RoleAdmin, h.start), middlewares...))
	r.GET(p+"/{id}", lib.ChainMiddlewares(h.role(auth.RoleViewer, h.get), middlewares...))
	r.POST(p+"/{id}/stop", lib.ChainMiddlewares(h.role(auth.RoleAdmin, h.stop), middlewares...))
}

const openModelsOrgValue = "heimdall.openmodels.org"

func (h *OpenModelsHandler) role(min auth.Role, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.api == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
				"open models run on the hosted platform and need its database and keystore master key (HEIMDALL_MASTER_KEY)")
			return
		}
		p := auth.PrincipalFrom(ctx)
		switch {
		case !p.IsAuthenticated() || p.Kind == auth.KindRunner:
			lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in to the organization")
			return
		case p.OrgID == "" || !p.Can(min):
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "open models need the "+string(min)+" role")
			return
		}
		ctx.SetUserValue(openModelsOrgValue, p.OrgID)
		ctx.Response.Header.Set("Cache-Control", "no-store")
		next(ctx)
	}
}

func openModelsOrg(ctx *fasthttp.RequestCtx) string {
	s, _ := ctx.UserValue(openModelsOrgValue).(string)
	return s
}

func (h *OpenModelsHandler) sendErr(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case errors.Is(err, openmodel.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": "+err.Error())
	case errors.Is(err, openmodel.ErrInvalid):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, openmodel.ErrNotConnected), errors.Is(err, openmodel.ErrNeedsHFSecret), errors.Is(err, sandbox.ErrNotConnected):
		lib.SendError(ctx, h.mode, fasthttp.StatusPreconditionFailed, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, openmodel.ErrTooMany):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		lib.SendError(ctx, h.mode, fasthttp.StatusGatewayTimeout, lib.ErrTypeUnavailable, what+": timed out")
	default:
		sendDepError(ctx, h.mode, what, err)
	}
}

func (h *OpenModelsHandler) write(ctx *fasthttp.RequestCtx, org, action, target string, details map[string]any) {
	if h.audit != nil {
		h.audit(ctx, org, action, "open_model", target, details)
	}
}

func (h *OpenModelsHandler) catalog(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, h.api.Catalog())
}

func (h *OpenModelsHandler) list(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.api.List(work, openModelsOrg(ctx))
	if err != nil {
		h.sendErr(ctx, "list open models", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, OpenModelsList{Deployments: out})
}

func (h *OpenModelsHandler) get(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.api.Get(work, openModelsOrg(ctx), id)
	if err != nil {
		h.sendErr(ctx, "open model "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *OpenModelsHandler) start(ctx *fasthttp.RequestCtx) {
	var req openmodel.StartRequest
	if err := decodeJSONStrict(bytes.TrimSpace(ctx.PostBody()), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	org := openModelsOrg(ctx)
	out, err := h.api.Start(work, org, auth.PrincipalFrom(ctx).ActorID(), req)
	if err != nil {
		h.sendErr(ctx, "start open model", err)
		return
	}
	h.write(ctx, org, "open_model.start", out.ID, map[string]any{"model": out.Model, "engine": out.Engine, "gpu": out.GPU,
		"gpu_count": out.GPUCount, "experiment_id": out.ExpID, "max_usd": out.MaxUSD})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, out)
}

// openModelStopTimeout bounds a stop: Modal's CLI stops the app.
const openModelStopTimeout = 5 * time.Minute

func (h *OpenModelsHandler) stop(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	work, cancel := workContext(ctx, openModelStopTimeout)
	defer cancel()
	org := openModelsOrg(ctx)
	out, err := h.api.Stop(work, org, id, auth.PrincipalFrom(ctx).ActorID())
	if err != nil {
		h.sendErr(ctx, "stop open model "+id, err)
		return
	}
	h.write(ctx, org, "open_model.stop", id, map[string]any{"state": out.State})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *OpenModelsHandler) apps(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, platformCheckTimeout)
	defer cancel()
	out, err := h.api.OrphanApps(work, openModelsOrg(ctx))
	if err != nil {
		h.sendErr(ctx, "list Modal apps", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, OpenModelApps{Apps: out})
}
