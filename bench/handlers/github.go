package handlers

// GitHub repository connection (web parity F6, lane L07): hosted platform
// routes that connect an org to GitHub with a repo-scope OAuth token (kept
// in the org's keystore), list its repositories and refs, and open the
// CI-install pull request. The token never leaves the server.

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/ci"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// GitHubPrefix is the prefix of the GitHub connect routes.
const GitHubPrefix = APIPrefix + "/github"

// HeimdallSource is the repository the CI workflow builds Heimdall from.
const HeimdallSource = "Ronit-Raj9/Heimdall"

const githubTimeout = 45 * time.Second

// GitHubHandler serves the GitHub connect routes.
type GitHubHandler struct {
	mode    schemas.Mode
	conn    *auth.GitHubRepoConnector
	keys    keystore.Store
	keysErr string // why keys is nil
	audit   tenancy.AuditSink
}

// NewGitHubHandler builds the handler. keys nil answers 503 with keysErr.
func NewGitHubHandler(mode schemas.Mode, conn *auth.GitHubRepoConnector, keys keystore.Store, keysErr string, audit tenancy.AuditSink) *GitHubHandler {
	return &GitHubHandler{mode: mode, conn: conn, keys: keys, keysErr: keysErr, audit: audit}
}

// RegisterRoutes registers the GitHub routes.
func (h *GitHubHandler) RegisterRoutes(r *router.Router, mw ...lib.Middleware) {
	c := func(f fasthttp.RequestHandler) fasthttp.RequestHandler { return lib.ChainMiddlewares(f, mw...) }
	r.GET(GitHubPrefix+"/status", c(h.getStatus))
	r.POST(GitHubPrefix+"/connect/start", c(h.postStart))
	r.GET(auth.GitHubRepoCallbackPath, c(h.getCallback))
	r.POST(GitHubPrefix+"/disconnect", c(h.postDisconnect))
	r.GET(GitHubPrefix+"/repos", c(h.getRepos))
	r.GET(GitHubPrefix+"/repos/{owner}/{repo}/refs", c(h.getRefs))
	r.GET(GitHubAgentFilePath, c(h.getAgentFile)) // lane Z15: the repository's .heimdall/agent.yaml (githubagentfile.go)
	r.POST(GitHubPrefix+"/ci-install", c(h.postCIInstall))
}

// GitHubStatus is GET /github/status.
type GitHubStatus struct {
	Configured  bool       `json:"configured"`
	Connected   bool       `json:"connected"`
	Login       string     `json:"login,omitempty"`
	ConnectedBy string     `json:"connected_by,omitempty"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
	Hint        string     `json:"hint,omitempty"`
}

// GitHubStartRequest is POST /github/connect/start.
type GitHubStartRequest struct {
	ReturnTo string `json:"return_to,omitempty"`
}

// GitHubStartResponse carries the GitHub authorize URL to navigate to.
type GitHubStartResponse struct {
	URL string `json:"url"`
}

// GitHubReposResponse is GET /github/repos.
type GitHubReposResponse struct {
	Repos []auth.GitHubRepo `json:"repos"`
	Page  int               `json:"page"`
	More  bool              `json:"more"`
}

// GitHubCIInstallRequest is POST /github/ci-install.
type GitHubCIInstallRequest struct {
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Base      string `json:"base,omitempty"`       // "" = the default branch
	Mode      string `json:"mode,omitempty"`       // recorded (default) | live
	BaseAgent string `json:"base_agent,omitempty"` // live: agents/<id>.yaml
	Force     bool   `json:"force,omitempty"`
}

var baseAgentRE = regexp.MustCompile(`^agents/[A-Za-z0-9_.-]{1,100}\.ya?ml$`)

func (h *GitHubHandler) fail(ctx *fasthttp.RequestCtx, status int, msg string) {
	t := lib.ErrTypeBadRequest
	switch status {
	case http.StatusUnauthorized:
		t = lib.ErrTypeUnauthorized
	case http.StatusForbidden:
		t = lib.ErrTypeForbidden
	case http.StatusNotFound:
		t = lib.ErrTypeNotFound
	case http.StatusConflict:
		t = lib.ErrTypeConflict
	case http.StatusServiceUnavailable:
		t = lib.ErrTypeUnavailable
	case http.StatusBadGateway, http.StatusInternalServerError:
		t = lib.ErrTypeInternal
	}
	lib.SendError(ctx, h.mode, status, t, msg)
}

// ready checks the connector and the keystore and returns the request's org.
func (h *GitHubHandler) ready(ctx *fasthttp.RequestCtx) (*tenancy.Org, bool) {
	o, ok := tenancy.OrgFrom(ctx)
	if !ok {
		h.fail(ctx, http.StatusForbidden, "request not resolved to an organization")
		return nil, false
	}
	if !h.conn.Configured() {
		h.fail(ctx, http.StatusServiceUnavailable, auth.ErrGitHubRepoDisabled.Error())
		return nil, false
	}
	if h.keys == nil {
		h.fail(ctx, http.StatusServiceUnavailable, "the key store is not available: "+h.keysErr)
		return nil, false
	}
	return o, true
}

// token returns the org's stored token, answering 409 when not connected.
func (h *GitHubHandler) token(ctx *fasthttp.RequestCtx, wctx context.Context, orgID string) (string, bool) {
	tok, _, err := auth.FindRepoToken(wctx, h.keys, orgID)
	if errors.Is(err, keystore.ErrNotFound) {
		h.fail(ctx, http.StatusConflict, "GitHub is not connected for this organization; an admin can connect it in Settings > GitHub")
		return "", false
	}
	if err != nil {
		h.fail(ctx, http.StatusInternalServerError, "read the stored GitHub token failed")
		return "", false
	}
	return tok, true
}

// apiErr maps a GitHub API error.
func (h *GitHubHandler) apiErr(ctx *fasthttp.RequestCtx, err error) {
	var ae *auth.GitHubAPIError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusUnauthorized:
			h.fail(ctx, http.StatusConflict, err.Error())
		case http.StatusBadRequest, http.StatusNotFound, http.StatusForbidden, http.StatusConflict:
			h.fail(ctx, ae.Status, err.Error())
		case http.StatusUnprocessableEntity:
			h.fail(ctx, http.StatusBadRequest, err.Error())
		default:
			h.fail(ctx, http.StatusBadGateway, err.Error())
		}
		return
	}
	if errors.Is(err, auth.ErrWorkflowExists) {
		h.fail(ctx, http.StatusConflict, err.Error())
		return
	}
	h.fail(ctx, http.StatusBadGateway, "GitHub request failed: "+err.Error())
}

func (h *GitHubHandler) record(ctx *fasthttp.RequestCtx, orgID, action, target string, details map[string]any) {
	if h.audit == nil {
		return
	}
	actx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	_ = h.audit.Audit(actx, tenancy.EntryFor(ctx, orgID, action, "github", target, details))
}

// getStatus handles GET /github/status.
func (h *GitHubHandler) getStatus(ctx *fasthttp.RequestCtx) {
	o, ok := tenancy.OrgFrom(ctx)
	if !ok {
		h.fail(ctx, http.StatusForbidden, "request not resolved to an organization")
		return
	}
	st := GitHubStatus{Configured: h.conn.Configured()}
	switch {
	case !st.Configured:
		st.Hint = auth.ErrGitHubRepoDisabled.Error()
	case h.keys == nil:
		st.Hint = "the key store is not available: " + h.keysErr
	default:
		wctx, cancel := workContext(ctx, readTimeout)
		defer cancel()
		if k, err := auth.RepoKey(wctx, h.keys, o.ID); err == nil {
			st.Connected, st.Login, st.ConnectedBy = true, k.Label, k.CreatedBy
			at := k.CreatedAt
			st.ConnectedAt = &at
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, st)
}

// postStart handles POST /github/connect/start: answers the GitHub
// authorize URL; the browser navigates there.
func (h *GitHubHandler) postStart(ctx *fasthttp.RequestCtx) {
	o, ok := h.ready(ctx)
	if !ok {
		return
	}
	var req GitHubStartRequest
	if len(ctx.PostBody()) > 0 {
		if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
			h.fail(ctx, http.StatusBadRequest, "parse request body: "+err.Error())
			return
		}
	}
	p := auth.PrincipalFrom(ctx)
	if p.Kind != auth.KindUser || p.UserID == "" {
		h.fail(ctx, http.StatusForbidden, "connect GitHub from a signed-in browser session, not an API token")
		return
	}
	if req.ReturnTo == "" {
		req.ReturnTo = "/settings?tab=github"
	}
	to, err := h.conn.Start(ctx, o.ID, p.UserID, req.ReturnTo)
	if err != nil {
		h.fail(ctx, http.StatusServiceUnavailable, err.Error())
		return
	}
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, GitHubStartResponse{URL: to})
}

// getCallback handles GitHub's redirect back. It always redirects to the
// return path with github=connected or github_error=<message>.
func (h *GitHubHandler) getCallback(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	back := func(returnTo, key, val string) {
		if returnTo == "" {
			returnTo = "/settings?tab=github"
		}
		sep := "?"
		if u, err := url.Parse(returnTo); err == nil && u.RawQuery != "" {
			sep = "&"
		}
		ctx.Response.Header.Set(fasthttp.HeaderLocation, h.conn.PublicURL()+returnTo+sep+key+"="+url.QueryEscape(val))
		ctx.SetStatusCode(fasthttp.StatusFound)
	}
	p := auth.PrincipalFrom(ctx)
	if h.keys == nil {
		back("", "github_error", "the key store is not available: "+h.keysErr)
		return
	}
	rt, err := h.conn.Callback(ctx, p.UserID)
	if err != nil {
		back("", "github_error", err.Error())
		return
	}
	wctx, cancel := workContext(ctx, githubTimeout)
	defer cancel()
	// One token per org: replace any earlier one.
	if old, err := auth.RepoKey(wctx, h.keys, rt.OrgID); err == nil {
		_ = h.keys.Delete(wctx, rt.OrgID, old.ID, p.ActorID())
	}
	if _, err := h.keys.Create(wctx, rt.OrgID, keystore.KindSecret, keystore.Put{
		Provider: auth.GitHubRepoProvider, Label: rt.Login, Value: rt.Token,
	}, p.ActorID()); err != nil {
		back(rt.Return, "github_error", "storing the GitHub token failed; try again")
		return
	}
	h.record(ctx, rt.OrgID, "github.connect", rt.Login, map[string]any{"scopes": rt.Scopes})
	back(rt.Return, "github", "connected")
}

// postDisconnect handles POST /github/disconnect: deletes the stored token.
func (h *GitHubHandler) postDisconnect(ctx *fasthttp.RequestCtx) {
	o, ok := tenancy.OrgFrom(ctx)
	if !ok {
		h.fail(ctx, http.StatusForbidden, "request not resolved to an organization")
		return
	}
	if h.keys == nil {
		h.fail(ctx, http.StatusServiceUnavailable, "the key store is not available: "+h.keysErr)
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	k, err := auth.RepoKey(wctx, h.keys, o.ID)
	if errors.Is(err, keystore.ErrNotFound) {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, GitHubStatus{Configured: h.conn.Configured()})
		return
	}
	if err == nil {
		err = h.keys.Delete(wctx, o.ID, k.ID, auth.PrincipalFrom(ctx).ActorID())
	}
	if err != nil {
		h.fail(ctx, http.StatusInternalServerError, "remove the stored GitHub token failed")
		return
	}
	h.record(ctx, o.ID, "github.disconnect", k.Label, nil)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, GitHubStatus{Configured: h.conn.Configured()})
}

// getRepos handles GET /github/repos?page=N.
func (h *GitHubHandler) getRepos(ctx *fasthttp.RequestCtx) {
	o, ok := h.ready(ctx)
	if !ok {
		return
	}
	page := 1
	if v := string(ctx.QueryArgs().Peek("page")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			h.fail(ctx, http.StatusBadRequest, "page must be a number from 1 to 100")
			return
		}
		page = n
	}
	wctx, cancel := workContext(ctx, githubTimeout)
	defer cancel()
	tok, ok := h.token(ctx, wctx, o.ID)
	if !ok {
		return
	}
	repos, more, err := h.conn.Repos(wctx, tok, page)
	if err != nil {
		h.apiErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, GitHubReposResponse{Repos: repos, Page: page, More: more})
}

// getRefs handles GET /github/repos/{owner}/{repo}/refs.
func (h *GitHubHandler) getRefs(ctx *fasthttp.RequestCtx) {
	o, ok := h.ready(ctx)
	if !ok {
		return
	}
	owner, repo := pathParam(ctx, "owner"), pathParam(ctx, "repo")
	if !auth.ValidRepo(owner, repo) {
		h.fail(ctx, http.StatusBadRequest, "invalid owner or repository name")
		return
	}
	wctx, cancel := workContext(ctx, githubTimeout)
	defer cancel()
	tok, ok := h.token(ctx, wctx, o.ID)
	if !ok {
		return
	}
	refs, err := h.conn.Refs(wctx, tok, owner, repo)
	if err != nil {
		h.apiErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, refs)
}

// postCIInstall handles POST /github/ci-install: opens a pull request that
// adds .github/workflows/heimdall.yml.
func (h *GitHubHandler) postCIInstall(ctx *fasthttp.RequestCtx) {
	o, ok := h.ready(ctx)
	if !ok {
		return
	}
	var req GitHubCIInstallRequest
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		h.fail(ctx, http.StatusBadRequest, "parse request body: "+err.Error())
		return
	}
	if !auth.ValidRepo(req.Owner, req.Repo) {
		h.fail(ctx, http.StatusBadRequest, "pick a repository: owner and repo are required")
		return
	}
	if req.Base != "" && !auth.ValidBranch(req.Base) {
		h.fail(ctx, http.StatusBadRequest, "invalid base branch name")
		return
	}
	if req.Mode == "" {
		req.Mode = ci.ModeRecorded
	}
	if req.Mode == ci.ModeLive && !baseAgentRE.MatchString(req.BaseAgent) {
		h.fail(ctx, http.StatusBadRequest, "live mode needs base_agent, the agent config both arms start from, like agents/my-agent.yaml")
		return
	}
	wf, err := ci.GitHubWorkflow(ci.WorkflowOptions{
		Mode: req.Mode, Source: HeimdallSource, SourceRef: "main", Base: req.BaseAgent,
		Trials: 3, Faults: []string{"model-outage"}, BudgetUSD: 5, DataMode: "recorded",
	})
	if err != nil {
		h.fail(ctx, http.StatusBadRequest, err.Error())
		return
	}
	wctx, cancel := workContext(ctx, githubTimeout)
	defer cancel()
	tok, ok := h.token(ctx, wctx, o.ID)
	if !ok {
		return
	}
	body := "This pull request adds the Heimdall pull-request gate (`.github/workflows/heimdall.yml`), opened from Heimdall Settings > GitHub.\n\n"
	if req.Mode == ci.ModeLive {
		body += "Live mode: register a self-hosted runner labelled `heimdall` with Docker and the stack up, and set the repository variable `HEIMDALL_ENV_FILE` to the stack's env file on it.\n"
	} else {
		body += "Recorded mode: commit one sealed experiment under `.heimdall/recorded/<exp_id>/` so the gate has something to re-check. No secrets are needed.\n"
	}
	res, err := h.conn.OpenCIInstallPR(wctx, tok, auth.CIInstall{
		Owner: req.Owner, Repo: req.Repo, Base: req.Base, Path: ".github/workflows/heimdall.yml",
		Content: wf, Title: "Add the Heimdall pull-request gate", Body: body, Force: req.Force,
	})
	if err != nil {
		h.apiErr(ctx, err)
		return
	}
	h.record(ctx, o.ID, "github.ci_install", req.Owner+"/"+req.Repo, map[string]any{"pr": res.Number, "mode": req.Mode})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, res)
}
