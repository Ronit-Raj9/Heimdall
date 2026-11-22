// Identity (Wave A, audit B1 and B10): email verification for password
// accounts and the server admin's signup mode setting. Hosted mode only;
// the routes are platform routes (cmd/bifrost-bench/identity_hosted.go)
// behind the auth middleware and the tenancy policy
// (tenancy/policy_identity.go).
package handlers

import (
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Identity route paths.
const (
	VerifyEmailPath   = AuthPrefix + "/verify-email"
	VerifyResendPath  = AuthPrefix + "/verify-email/resend"
	SignupSettingPath = APIPrefix + "/admin/signup"
)

// IdentityHandler serves the verification and signup setting routes.
type IdentityHandler struct {
	mode schemas.Mode
	svc  *auth.Service
}

// NewIdentityHandler creates the handler; svc must be the hosted auth
// service.
func NewIdentityHandler(mode schemas.Mode, svc *auth.Service) *IdentityHandler {
	return &IdentityHandler{mode: mode, svc: svc}
}

// RegisterRoutes registers the identity routes.
func (h *IdentityHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.POST(VerifyEmailPath, lib.ChainMiddlewares(h.postVerify, middlewares...))
	r.POST(VerifyResendPath, lib.ChainMiddlewares(h.postResend, middlewares...))
	r.GET(SignupSettingPath, lib.ChainMiddlewares(h.getSignup, middlewares...))
	r.POST(SignupSettingPath, lib.ChainMiddlewares(h.postSignup, middlewares...))
}

func (h *IdentityHandler) ready(ctx *fasthttp.RequestCtx) bool {
	if h.svc == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"email verification and signup settings need hosted mode (auth.mode: hosted)")
		return false
	}
	return true
}

func (h *IdentityHandler) fail(ctx *fasthttp.RequestCtx, err error) {
	if h.svc == nil {
		auth.SendError(ctx, h.mode, err)
		return
	}
	h.svc.Fail(ctx, err)
}

// postVerify handles POST /auth/verify-email: the token from the link. No
// session is needed; the token is the credential and works once.
func (h *IdentityHandler) postVerify(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	var req auth.VerifyRequest
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request body: "+err.Error())
		return
	}
	w, cancel := workContext(ctx, authTimeout)
	defer cancel()
	out, err := h.svc.VerifyEmail(w, req, h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// postResend handles POST /auth/verify-email/resend: a new link for the
// signed-in user, mailed or (with no mail sender) written to the server
// log. The link itself is never in the answer.
func (h *IdentityHandler) postResend(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	w, cancel := workContext(ctx, authTimeout)
	defer cancel()
	out, err := h.svc.SendVerification(w, auth.PrincipalFrom(ctx), h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *IdentityHandler) serverAdmin(ctx *fasthttp.RequestCtx) bool {
	if !auth.IsServerAdmin(auth.PrincipalFrom(ctx)) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"server admins only: run `heimdall admin grant -email you@example.com` on the server, or list your verified email in "+auth.ServerAdminsEnv)
		return false
	}
	return true
}

// getSignup handles GET /admin/signup (server admin): the effective signup
// mode, where it comes from, and how verification mail is delivered.
func (h *IdentityHandler) getSignup(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) || !h.serverAdmin(ctx) {
		return
	}
	w, cancel := workContext(ctx, authTimeout)
	defer cancel()
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, h.svc.SignupSettings(w))
}

// postSignup handles POST /admin/signup (server admin): override the signup
// mode and allowed domains, or (empty mode) go back to the server config.
func (h *IdentityHandler) postSignup(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) || !h.serverAdmin(ctx) {
		return
	}
	var req auth.SignupSettingsRequest
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request body: "+err.Error())
		return
	}
	w, cancel := workContext(ctx, authTimeout)
	defer cancel()
	out, err := h.svc.SetSignupSettings(w, auth.PrincipalFrom(ctx), req, h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
