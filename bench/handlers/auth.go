// Auth area (P2): sign-up, sign-in, sessions, API tokens, invitation
// acceptance and GitHub sign-in under /api/bench/auth/ (docs/auth.md).
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// AuthPrefix is where the auth routes live.
const AuthPrefix = APIPrefix + "/auth"

// authTimeout bounds one auth request's database work. Password hashing
// (argon2id) runs outside it.
const authTimeout = 20 * time.Second

// AuthHandler serves /api/bench/auth/*. In local mode (svc nil) only
// GET /auth/config and GET /auth/me answer, describing the local principal;
// the hosted-only routes answer 404.
type AuthHandler struct {
	mode schemas.Mode
	svc  *auth.Service
}

// NewAuthHandler creates the auth handler. svc is nil in local mode.
func NewAuthHandler(mode schemas.Mode, svc *auth.Service) *AuthHandler {
	return &AuthHandler{mode: mode, svc: svc}
}

// TokenList is GET /auth/tokens.
type TokenList struct {
	Tokens []auth.APIToken `json:"tokens"`
}

// RegisterRoutes registers every /api/bench/auth/ route. The routes are
// served directly, never through an org router: the auth middleware
// (auth.Middleware) must have run before them.
func (h *AuthHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	p := AuthPrefix
	r.GET(p+"/config", lib.ChainMiddlewares(h.getConfig, middlewares...))
	r.GET(p+"/me", lib.ChainMiddlewares(h.getMe, middlewares...))
	r.POST(p+"/signup", lib.ChainMiddlewares(h.hosted(h.postSignup), middlewares...))
	r.POST(p+"/login", lib.ChainMiddlewares(h.hosted(h.postLogin), middlewares...))
	r.POST(p+"/logout", lib.ChainMiddlewares(h.hosted(h.postLogout), middlewares...))
	r.POST(p+"/switch-org", lib.ChainMiddlewares(h.hosted(h.postSwitchOrg), middlewares...))
	r.POST(p+"/password", lib.ChainMiddlewares(h.hosted(h.postPassword), middlewares...))
	r.GET(p+"/github/start", lib.ChainMiddlewares(h.hosted(h.getGitHubStart), middlewares...))
	r.GET(p+"/github/callback", lib.ChainMiddlewares(h.hosted(h.getGitHubCallback), middlewares...))
	r.GET(p+"/invitations/{token}", lib.ChainMiddlewares(h.hosted(h.getInvitation), middlewares...))
	r.POST(p+"/invitations/{token}", lib.ChainMiddlewares(h.hosted(h.postInvitation), middlewares...))
	r.GET(p+"/tokens", lib.ChainMiddlewares(h.hosted(h.getTokens), middlewares...))
	r.POST(p+"/tokens", lib.ChainMiddlewares(h.hosted(h.postToken), middlewares...))
	r.POST(p+"/tokens/{id}/revoke", lib.ChainMiddlewares(h.hosted(h.postTokenRevoke), middlewares...))
}

// hosted answers 404 on a hosted-only route in local mode.
func (h *AuthHandler) hosted(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.svc == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
				"route only exists in hosted mode (auth.mode: hosted): "+string(ctx.Path()))
			return
		}
		next(ctx)
	}
}

func (h *AuthHandler) work() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), authTimeout)
}

// decodeAuthBody parses a JSON body strictly, answering 400 on failure.
func (h *AuthHandler) decodeAuthBody(ctx *fasthttp.RequestCtx, dst any) bool {
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request body: "+err.Error())
		return false
	}
	return true
}

func (h *AuthHandler) fail(ctx *fasthttp.RequestCtx, err error) {
	if h.svc != nil {
		h.svc.Fail(ctx, err)
		return
	}
	auth.SendError(ctx, h.mode, err)
}

// getConfig handles GET /auth/config: the sign-in options.
func (h *AuthHandler) getConfig(ctx *fasthttp.RequestCtx) {
	if h.svc == nil {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, auth.AuthInfo{AuthMode: lib.AuthModeLocal})
		return
	}
	w, cancel := h.work()
	defer cancel()
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, h.svc.Info(w))
}

// getMe handles GET /auth/me: the caller, their orgs and roles, the current
// org and the session's CSRF value.
func (h *AuthHandler) getMe(ctx *fasthttp.RequestCtx) {
	if h.svc == nil {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, auth.LocalMe())
		return
	}
	w, cancel := h.work()
	defer cancel()
	me, err := h.svc.Me(w, auth.PrincipalFrom(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, me)
}

// postSignup handles POST /auth/signup and signs the new user in.
func (h *AuthHandler) postSignup(ctx *fasthttp.RequestCtx) {
	var req auth.SignupRequest
	if !h.decodeAuthBody(ctx, &req) {
		return
	}
	w, cancel := h.work()
	defer cancel()
	u, sess, err := h.svc.Signup(w, req, h.svc.ClientIP(ctx), string(ctx.UserAgent()), auth.PrincipalFrom(ctx).SessionID())
	if err != nil {
		h.fail(ctx, err)
		return
	}
	h.signedIn(ctx, w, u, sess, fasthttp.StatusCreated)
}

// postLogin handles POST /auth/login. Every failure answers the same 401.
func (h *AuthHandler) postLogin(ctx *fasthttp.RequestCtx) {
	var req auth.LoginRequest
	if !h.decodeAuthBody(ctx, &req) {
		return
	}
	w, cancel := h.work()
	defer cancel()
	u, sess, err := h.svc.Login(w, req, h.svc.ClientIP(ctx), string(ctx.UserAgent()), auth.PrincipalFrom(ctx).SessionID())
	if err != nil {
		h.fail(ctx, err)
		return
	}
	h.signedIn(ctx, w, u, sess, fasthttp.StatusOK)
}

// signedIn sets the session cookie and answers with Me.
func (h *AuthHandler) signedIn(ctx *fasthttp.RequestCtx, w context.Context, u *auth.User, sess *auth.NewSession, status int) {
	h.svc.SetSessionCookie(ctx, sess)
	me, err := h.svc.SessionMe(w, u, sess)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, status, me)
}

// postLogout handles POST /auth/logout: ends the session and clears the
// cookie. Idempotent.
func (h *AuthHandler) postLogout(ctx *fasthttp.RequestCtx) {
	w, cancel := h.work()
	defer cancel()
	if err := h.svc.Logout(w, auth.PrincipalFrom(ctx), h.svc.ClientIP(ctx)); err != nil {
		h.fail(ctx, err)
		return
	}
	h.svc.ClearSessionCookie(ctx)
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// postSwitchOrg handles POST /auth/switch-org.
func (h *AuthHandler) postSwitchOrg(ctx *fasthttp.RequestCtx) {
	var req auth.SwitchOrgRequest
	if !h.decodeAuthBody(ctx, &req) {
		return
	}
	w, cancel := h.work()
	defer cancel()
	o, err := h.svc.SwitchOrg(w, auth.PrincipalFrom(ctx), req.Org, h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, o)
}

// postPassword handles POST /auth/password.
func (h *AuthHandler) postPassword(ctx *fasthttp.RequestCtx) {
	var req auth.PasswordChangeRequest
	if !h.decodeAuthBody(ctx, &req) {
		return
	}
	w, cancel := h.work()
	defer cancel()
	if err := h.svc.ChangePassword(w, auth.PrincipalFrom(ctx), req, h.svc.ClientIP(ctx)); err != nil {
		h.fail(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// getGitHubStart handles GET /auth/github/start?return_to=/path: redirects
// to GitHub.
func (h *AuthHandler) getGitHubStart(ctx *fasthttp.RequestCtx) {
	to, err := h.svc.GitHubStart(ctx, string(ctx.QueryArgs().Peek("return_to")))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	ctx.Response.Header.Set("Cache-Control", "no-store")
	h.redirect(ctx, to)
}

// getGitHubCallback handles GitHub's redirect back. It always answers with
// a redirect: to the return path on success, to /login?error=github
// otherwise (details go to the server log and the audit log).
func (h *AuthHandler) getGitHubCallback(ctx *fasthttp.RequestCtx) {
	sess, returnTo, err := h.svc.GitHubCallback(ctx, auth.PrincipalFrom(ctx).SessionID())
	ctx.Response.Header.Set("Cache-Control", "no-store")
	if err != nil {
		reason := "github"
		if errors.Is(err, auth.ErrSignupNotAllowed) {
			reason = "signup_not_allowed"
		}
		h.redirect(ctx, h.svc.Config().PublicURL+"/login?error="+url.QueryEscape(reason))
		return
	}
	h.svc.SetSessionCookie(ctx, sess)
	h.redirect(ctx, h.svc.Config().PublicURL+returnTo)
}

// redirect answers 302 to an absolute URL as given. fasthttp's own
// Redirect would rebuild it from the request, which behind a TLS-ending
// proxy says http.
func (h *AuthHandler) redirect(ctx *fasthttp.RequestCtx, to string) {
	ctx.Response.Header.Set(fasthttp.HeaderLocation, to)
	ctx.SetStatusCode(fasthttp.StatusFound)
}

// getInvitation handles GET /auth/invitations/{token}.
func (h *AuthHandler) getInvitation(ctx *fasthttp.RequestCtx) {
	w, cancel := h.work()
	defer cancel()
	info, err := h.svc.InvitationInfo(w, pathParam(ctx, "token"), h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, info)
}

// postInvitation handles POST /auth/invitations/{token}: the signed-in user
// joins the org. New users sign up with the token instead.
func (h *AuthHandler) postInvitation(ctx *fasthttp.RequestCtx) {
	w, cancel := h.work()
	defer cancel()
	o, err := h.svc.AcceptInvitation(w, auth.PrincipalFrom(ctx), pathParam(ctx, "token"), h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, o)
}

// getTokens handles GET /auth/tokens: the current org's API tokens.
func (h *AuthHandler) getTokens(ctx *fasthttp.RequestCtx) {
	w, cancel := h.work()
	defer cancel()
	list, err := h.svc.ListTokens(w, auth.PrincipalFrom(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, TokenList{Tokens: list})
}

// postToken handles POST /auth/tokens. The token is in this answer only.
func (h *AuthHandler) postToken(ctx *fasthttp.RequestCtx) {
	var req auth.TokenCreateRequest
	if !h.decodeAuthBody(ctx, &req) {
		return
	}
	w, cancel := h.work()
	defer cancel()
	created, err := h.svc.CreateToken(w, auth.PrincipalFrom(ctx), req, h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, created)
}

// postTokenRevoke handles POST /auth/tokens/{id}/revoke.
func (h *AuthHandler) postTokenRevoke(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.mode, "id", "API token")
	if !ok {
		return
	}
	w, cancel := h.work()
	defer cancel()
	if err := h.svc.RevokeToken(w, auth.PrincipalFrom(ctx), id, h.svc.ClientIP(ctx)); err != nil {
		h.fail(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}
