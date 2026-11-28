package handlers

// Lane Z14: the public status page. GET /status answers each platform
// component's health and the operator's incident notes (bench/statuspage).
// It needs no session, token or org: the policy entry is public, and the
// page says nothing about any org.

import (
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/statuspage"
	"github.com/valyala/fasthttp"
)

// StatusPagePath is the public status route.
const StatusPagePath = APIPrefix + "/status"

// StatusPageHandler serves GET /status.
type StatusPageHandler struct {
	mode schemas.Mode
	svc  *statuspage.Service
}

// NewStatusPageHandler creates the handler; a nil service answers 503.
func NewStatusPageHandler(mode schemas.Mode, svc *statuspage.Service) *StatusPageHandler {
	return &StatusPageHandler{mode: mode, svc: svc}
}

// RegisterRoutes registers GET /status.
func (h *StatusPageHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(StatusPagePath, lib.ChainMiddlewares(h.get, middlewares...))
	h.registerIncidentRoutes(r, middlewares...)
}

func (h *StatusPageHandler) get(ctx *fasthttp.RequestCtx) {
	if h.svc == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "the status page is not configured on this server")
		return
	}
	ctx.Response.Header.Set("Cache-Control", "public, max-age=15")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, h.svc.Page(ctx))
}
