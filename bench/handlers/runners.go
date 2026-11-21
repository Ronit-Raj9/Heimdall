package handlers

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// RunnerAdmin is the runners area (package runners) as the admin routes use
// it. Every call is scoped to one organization. A nil RunnerAdmin means the
// server is not hosted: the routes answer 503 (local mode uses
// BENCH_WORKER_TOKEN, docs/remote-execution.md).
type RunnerAdmin interface {
	ListRunners(ctx context.Context, orgID string) ([]schemas.RunnerView, error)
	GetRunner(ctx context.Context, orgID, id string) (*schemas.RunnerView, error)
	RemoveRunner(ctx context.Context, orgID, id string) (*schemas.RunnerView, error)
	ListTokens(ctx context.Context, orgID string) ([]schemas.RunnerTokenView, error)
	CreateToken(ctx context.Context, orgID, createdBy string, req schemas.RunnerTokenCreate) (*schemas.RunnerTokenCreated, error)
	RevokeToken(ctx context.Context, orgID, id string) (*schemas.RunnerTokenView, error)
}

// AuditFunc writes one audit row for a write the request made
// (docs/platform-design.md rule 6). Serve wires it to the database area's
// audit sink; nil writes nothing.
type AuditFunc func(ctx *fasthttp.RequestCtx, orgID, action, targetType, targetID string, details map[string]any)

// RunnersHandler serves the runner admin API under /api/bench/runners
// (Runners area, docs/runners.md): list and remove runners, create and
// revoke runner tokens, and the runner install script. Every route but the
// install script needs an admin of the request's organization.
type RunnersHandler struct {
	mode    schemas.Mode
	api     RunnerAdmin
	audit   AuditFunc
	install []byte
}

// NewRunnersHandler creates the runner admin handler.
func NewRunnersHandler(mode schemas.Mode, api RunnerAdmin, audit AuditFunc) *RunnersHandler {
	return &RunnersHandler{mode: mode, api: api, audit: audit}
}

// WithInstallScript serves script at GET /api/bench/runners/install.sh.
func (h *RunnersHandler) WithInstallScript(script []byte) *RunnersHandler {
	h.install = script
	return h
}

// WithInstallDefaults fills the install script's defaults (the
// HEIMDALL_DEFAULT_* lines) with this server's address, the repository and
// the commit runners must run, so the one-line install
// (curl .../install.sh | sudo HEIMDALL_RUNNER_TOKEN=... sh) needs nothing
// else. A value that is not plainly safe in a shell assignment is left out.
// Call it after WithInstallScript.
func (h *RunnersHandler) WithInstallDefaults(server, repo, ref string) *RunnersHandler {
	h.install = InstallScriptWithDefaults(h.install, server, repo, ref)
	return h
}

var (
	installServerRe = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$`)
	installRepoRe   = regexp.MustCompile(`^(https://|ssh://|git@)[A-Za-z0-9._~:/@+-]+$`)
	installRefRe    = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,128}$`)
)

// InstallScriptWithDefaults returns script with its HEIMDALL_DEFAULT_*
// assignments set to the values that pass their pattern.
func InstallScriptWithDefaults(script []byte, server, repo, ref string) []byte {
	out := script
	set := func(name, v string, re *regexp.Regexp) {
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v == "" || !re.MatchString(v) || strings.Contains(v, "..") && name == "HEIMDALL_DEFAULT_REF" {
			return
		}
		out = bytes.Replace(out, []byte("\n"+name+"=\"\"\n"), []byte("\n"+name+"='"+v+"'\n"), 1)
	}
	set("HEIMDALL_DEFAULT_SERVER", server, installServerRe)
	set("HEIMDALL_DEFAULT_REPO", repo, installRepoRe)
	set("HEIMDALL_DEFAULT_REF", ref, installRefRe)
	return out
}

// RegisterRoutes registers the runner admin routes.
func (h *RunnersHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	p := APIPrefix + "/runners"
	r.GET(p, lib.ChainMiddlewares(h.admin(h.list), middlewares...))
	r.GET(p+"/tokens", lib.ChainMiddlewares(h.admin(h.listTokens), middlewares...))
	r.POST(p+"/tokens", lib.ChainMiddlewares(h.admin(h.createToken), middlewares...))
	r.POST(p+"/tokens/{id}/revoke", lib.ChainMiddlewares(h.admin(h.revokeToken), middlewares...))
	r.GET(p+"/install.sh", lib.ChainMiddlewares(h.installScript, middlewares...))
	r.GET(p+"/{id}", lib.ChainMiddlewares(h.admin(h.get), middlewares...))
	r.POST(p+"/{id}/remove", lib.ChainMiddlewares(h.admin(h.remove), middlewares...))
}

// orgKey carries the admin's organization from admin to the handlers.
const runnersOrgValue = "heimdall.runners.org"

// admin answers 503 off the hosted platform, 401 without a signed-in caller
// and 403 unless the caller is an admin of its organization.
func (h *RunnersHandler) admin(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.api == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
				"runner admin is part of the hosted platform (auth.mode: hosted); local mode uses BENCH_WORKER_TOKEN")
			return
		}
		p := auth.PrincipalFrom(ctx)
		switch {
		case !p.IsAuthenticated() || p.Kind == auth.KindRunner:
			lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in as an admin of the organization")
			return
		case p.OrgID == "" || !p.Can(auth.RoleAdmin):
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "workers and worker tokens need the admin role")
			return
		}
		ctx.SetUserValue(runnersOrgValue, p.OrgID)
		next(ctx)
	}
}

func orgOf(ctx *fasthttp.RequestCtx) string {
	s, _ := ctx.UserValue(runnersOrgValue).(string)
	return s
}

func (h *RunnersHandler) list(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.api.ListRunners(work, orgOf(ctx))
	if err != nil {
		h.sendErr(ctx, "list runners", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.RunnersList{Runners: out})
}

func (h *RunnersHandler) get(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.mode, "id", "runner")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.api.GetRunner(work, orgOf(ctx), id)
	if err != nil {
		h.sendErr(ctx, "runner", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *RunnersHandler) remove(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.mode, "id", "runner")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	org := orgOf(ctx)
	out, err := h.api.RemoveRunner(work, org, id)
	if err != nil {
		h.sendErr(ctx, "remove runner", err)
		return
	}
	h.write(ctx, org, "runner.remove", "runner", id, map[string]any{"name": out.Name, "pool": out.Pool, "token_id": out.TokenID})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *RunnersHandler) listTokens(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.api.ListTokens(work, orgOf(ctx))
	if err != nil {
		h.sendErr(ctx, "list runner tokens", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.RunnerTokensList{Tokens: out})
}

// createToken answers the new token once, with the commands that start a
// runner with it. The token is never logged or audited.
func (h *RunnersHandler) createToken(ctx *fasthttp.RequestCtx) {
	var req schemas.RunnerTokenCreate
	if body := bytes.TrimSpace(ctx.PostBody()); len(body) > 0 {
		if err := decodeJSONStrict(body, &req); err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
			return
		}
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	org := orgOf(ctx)
	out, err := h.api.CreateToken(work, org, auth.PrincipalFrom(ctx).ActorID(), req)
	if err != nil {
		h.sendErr(ctx, "create runner token", err)
		return
	}
	h.write(ctx, org, "runner_token.create", "runner_token", out.Info.ID, map[string]any{
		"pool": out.Info.Pool, "name": out.Info.Name, "prefix": out.Info.Prefix, "expires_at": out.Info.ExpiresAt})
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, out)
}

func (h *RunnersHandler) revokeToken(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.mode, "id", "runner token")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	org := orgOf(ctx)
	out, err := h.api.RevokeToken(work, org, id)
	if err != nil {
		h.sendErr(ctx, "revoke runner token", err)
		return
	}
	h.write(ctx, org, "runner_token.revoke", "runner_token", id, map[string]any{"pool": out.Pool, "prefix": out.Prefix})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// installScript serves deploy/runner/install.sh. It holds no secret.
func (h *RunnersHandler) installScript(ctx *fasthttp.RequestCtx) {
	if len(h.install) == 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "this server does not serve the runner install script")
		return
	}
	ctx.SetContentType("text/x-shellscript; charset=utf-8")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(h.install)
}

func (h *RunnersHandler) write(ctx *fasthttp.RequestCtx, org, action, targetType, targetID string, details map[string]any) {
	if h.audit != nil {
		h.audit(ctx, org, action, targetType, targetID, details)
	}
}

func (h *RunnersHandler) sendErr(ctx *fasthttp.RequestCtx, what string, err error) {
	if errors.Is(err, schemas.ErrWorkerRequest) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
		return
	}
	sendDepError(ctx, h.mode, what, err)
}
