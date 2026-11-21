package handlers

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// WorkerPlatforms is an organization's cloud sandbox connections
// (sandbox.Connections) as the routes use them.
type WorkerPlatforms interface {
	List(ctx context.Context, org string) (*sandbox.ConnectionsList, error)
	Connect(ctx context.Context, org, name, token, endpoint, actor string) (*sandbox.ConnectionView, error)
	Test(ctx context.Context, org, name string) (*sandbox.ConnectionView, error)
	SetDefault(ctx context.Context, org, name, actor string) (*sandbox.ConnectionsList, error)
	Disconnect(ctx context.Context, org, name, actor string) (*sandbox.ConnectionsList, error)
}

// WorkerPlatformsHandler serves Settings, Workers, Connect a platform under
// /api/bench/worker-platforms (bring your own worker, docs/runners.md):
// connect Modal, Daytona or E2B with the org's own token (tested before it
// is kept), test it again, choose the org's default worker, disconnect.
// Every route needs an admin of the request's organization. Tokens go in,
// never out: answers carry the last four characters only.
type WorkerPlatformsHandler struct {
	mode  schemas.Mode
	api   WorkerPlatforms
	audit AuditFunc
}

// NewWorkerPlatformsHandler creates the handler; a nil api answers 503
// (local mode, or a server without a keystore master key).
func NewWorkerPlatformsHandler(mode schemas.Mode, api WorkerPlatforms, audit AuditFunc) *WorkerPlatformsHandler {
	return &WorkerPlatformsHandler{mode: mode, api: api, audit: audit}
}

// platformCheckTimeout bounds a connect or test call: the platform check
// (Modal starts its SDK) can take longer than a plain read.
const platformCheckTimeout = 3 * time.Minute

// RegisterRoutes registers the routes.
func (h *WorkerPlatformsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	p := APIPrefix + "/worker-platforms"
	r.GET(p, lib.ChainMiddlewares(h.admin(h.list), middlewares...))
	r.POST(p+"/default", lib.ChainMiddlewares(h.admin(h.setDefault), middlewares...))
	r.POST(p+"/{platform}/connect", lib.ChainMiddlewares(h.admin(h.connect), middlewares...))
	r.POST(p+"/{platform}/test", lib.ChainMiddlewares(h.admin(h.test), middlewares...))
	r.POST(p+"/{platform}/disconnect", lib.ChainMiddlewares(h.admin(h.disconnect), middlewares...))
}

const platformsOrgValue = "heimdall.platforms.org"

func (h *WorkerPlatformsHandler) admin(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.api == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
				"cloud sandbox platforms are part of the hosted platform and need the keystore master key (HEIMDALL_MASTER_KEY)")
			return
		}
		p := auth.PrincipalFrom(ctx)
		switch {
		case !p.IsAuthenticated() || p.Kind == auth.KindRunner:
			lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in as an admin of the organization")
			return
		case p.OrgID == "" || !p.Can(auth.RoleAdmin):
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "worker platforms need the admin role")
			return
		}
		ctx.SetUserValue(platformsOrgValue, p.OrgID)
		ctx.Response.Header.Set("Cache-Control", "no-store")
		next(ctx)
	}
}

func platformOrg(ctx *fasthttp.RequestCtx) string {
	s, _ := ctx.UserValue(platformsOrgValue).(string)
	return s
}

func (h *WorkerPlatformsHandler) platform(ctx *fasthttp.RequestCtx) (string, bool) {
	name, _ := ctx.UserValue("platform").(string)
	if !sandbox.IsPlatform(name) {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "unknown worker platform: "+name)
		return "", false
	}
	return name, true
}

func (h *WorkerPlatformsHandler) write(ctx *fasthttp.RequestCtx, org, action, target string, details map[string]any) {
	if h.audit != nil {
		h.audit(ctx, org, action, "worker_platform", target, details)
	}
}

func (h *WorkerPlatformsHandler) sendErr(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case errors.Is(err, sandbox.ErrNotConnected):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": "+err.Error())
	case errors.Is(err, sandbox.ErrUnknownPlatform):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": "+err.Error())
	case errors.Is(err, keystore.ErrInvalid):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		lib.SendError(ctx, h.mode, fasthttp.StatusGatewayTimeout, lib.ErrTypeUnavailable, what+": timed out")
	default:
		// A refused or unreachable platform is the caller's input, not a
		// server fault: 422 with the platform's (token free) reason.
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, what+": "+err.Error())
	}
}

func (h *WorkerPlatformsHandler) list(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.api.List(work, platformOrg(ctx))
	if err != nil {
		sendDepError(ctx, h.mode, "list worker platforms", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// PlatformConnectRequest is the connect body.
type PlatformConnectRequest struct {
	Token    string `json:"token"`
	Endpoint string `json:"endpoint,omitempty"`
}

func (h *WorkerPlatformsHandler) connect(ctx *fasthttp.RequestCtx) {
	name, ok := h.platform(ctx)
	if !ok {
		return
	}
	var req PlatformConnectRequest
	if err := decodeJSONStrict(bytes.TrimSpace(ctx.PostBody()), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, platformCheckTimeout)
	defer cancel()
	org := platformOrg(ctx)
	out, err := h.api.Connect(work, org, name, req.Token, req.Endpoint, auth.PrincipalFrom(ctx).ActorID())
	if err != nil {
		h.sendErr(ctx, "connect "+name, err)
		return
	}
	h.write(ctx, org, "worker_platform.connect", name, map[string]any{"token_last4": out.TokenLast4, "endpoint": out.Endpoint})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *WorkerPlatformsHandler) test(ctx *fasthttp.RequestCtx) {
	name, ok := h.platform(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, platformCheckTimeout)
	defer cancel()
	out, err := h.api.Test(work, platformOrg(ctx), name)
	if err != nil {
		h.sendErr(ctx, "test "+name, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// PlatformDefaultRequest is the default body: "" is the shared worker.
type PlatformDefaultRequest struct {
	Platform string `json:"platform"`
}

func (h *WorkerPlatformsHandler) setDefault(ctx *fasthttp.RequestCtx) {
	var req PlatformDefaultRequest
	if err := decodeJSONStrict(bytes.TrimSpace(ctx.PostBody()), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	org := platformOrg(ctx)
	out, err := h.api.SetDefault(work, org, req.Platform, auth.PrincipalFrom(ctx).ActorID())
	if err != nil {
		h.sendErr(ctx, "set default worker", err)
		return
	}
	target := req.Platform
	if target == "" {
		target = "shared"
	}
	h.write(ctx, org, "worker_platform.default", target, nil)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *WorkerPlatformsHandler) disconnect(ctx *fasthttp.RequestCtx) {
	name, ok := h.platform(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	org := platformOrg(ctx)
	out, err := h.api.Disconnect(work, org, name, auth.PrincipalFrom(ctx).ActorID())
	if err != nil {
		h.sendErr(ctx, "disconnect "+name, err)
		return
	}
	h.write(ctx, org, "worker_platform.disconnect", name, nil)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
