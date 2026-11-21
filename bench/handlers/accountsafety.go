// Account safety (L22): owner-made one-time password reset links and the
// public reset page's API. Hosted mode only; the routes are platform routes
// (cmd/bifrost-bench/accountsafety_hosted.go) behind the auth middleware and
// the tenancy policy (tenancy/policy_accountsafety.go).
package handlers

import (
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// AccountSafetyHandler serves the reset-link routes.
type AccountSafetyHandler struct {
	mode schemas.Mode
	svc  *auth.Service
}

// NewAccountSafetyHandler creates the handler; svc must be the hosted auth
// service.
func NewAccountSafetyHandler(mode schemas.Mode, svc *auth.Service) *AccountSafetyHandler {
	return &AccountSafetyHandler{mode: mode, svc: svc}
}

// ResetLinkPath is the owner route that makes a reset link.
const ResetLinkPath = APIPrefix + "/orgs/{org}/members/{user}/reset-link"

// ResetTokenPath is the public route the reset page reads and posts to.
const ResetTokenPath = AuthPrefix + "/reset/{token}"

// RegisterRoutes registers the account-safety routes.
func (h *AccountSafetyHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.POST(ResetLinkPath, lib.ChainMiddlewares(h.postResetLink, middlewares...))
	r.GET(ResetTokenPath, lib.ChainMiddlewares(h.getReset, middlewares...))
	r.POST(ResetTokenPath, lib.ChainMiddlewares(h.postReset, middlewares...))
}

func (h *AccountSafetyHandler) fail(ctx *fasthttp.RequestCtx, err error) {
	if h.svc == nil {
		auth.SendError(ctx, h.mode, err)
		return
	}
	h.svc.Fail(ctx, err)
}

func (h *AccountSafetyHandler) ready(ctx *fasthttp.RequestCtx) bool {
	if h.svc == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"password reset needs hosted mode (auth.mode: hosted); on a local server use `bifrost-bench user password`")
		return false
	}
	return true
}

// postResetLink handles POST /orgs/{org}/members/{user}/reset-link (owner):
// a one-time link the owner passes to the member. The token is in this
// answer only and is never logged.
func (h *AccountSafetyHandler) postResetLink(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	o, ok := tenancy.OrgFrom(ctx)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "request not resolved to an organization")
		return
	}
	userID, ok := idParam(ctx, h.mode, "user", "member")
	if !ok {
		return
	}
	w, cancel := workContext(ctx, authTimeout)
	defer cancel()
	link, err := h.svc.CreateResetLink(w, auth.PrincipalFrom(ctx), o.ID, userID, h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, link)
}

// getReset handles GET /auth/reset/{token}: the account the link resets.
func (h *AccountSafetyHandler) getReset(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	w, cancel := workContext(ctx, authTimeout)
	defer cancel()
	info, err := h.svc.LookupReset(w, pathParam(ctx, "token"), h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, info)
}

// postReset handles POST /auth/reset/{token}: sets the new password, uses
// the link up and ends every session of the account.
func (h *AccountSafetyHandler) postReset(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	var req auth.ResetRequest
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request body: "+err.Error())
		return
	}
	w, cancel := workContext(ctx, authTimeout)
	defer cancel()
	if err := h.svc.ResetPassword(w, pathParam(ctx, "token"), req, h.svc.ClientIP(ctx)); err != nil {
		h.fail(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}
