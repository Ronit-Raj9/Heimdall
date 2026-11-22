package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Pull-mode agents (lane Y11, docs/pull-agents.md). Two sides meet here:
//
//   - the agent's own process (heimdall.serve in the SDKs), with an org API
//     token: POST /pull/next takes the next turn of one agent (a long poll),
//     POST /pull/turns/{id}/answer answers it, GET /pull/agents lists the
//     org's serving processes;
//   - a run's launcher, with the run's pull token (signed at plan time, so
//     these routes need no session): POST /pull/run/turns queues a turn,
//     GET /pull/run/turns/{id} waits for its answer, POST /pull/run/repos
//     stores the run's workspace tarball for a coding agent;
//
// and GET /pull/repos/{id}?exp=&sig= serves a tarball to whoever holds the
// signed, expiring link.

// Pull routes.
const (
	PullNextPath     = APIPrefix + "/pull/next"
	PullAnswerPath   = APIPrefix + "/pull/turns/{id}/answer"
	PullAgentsPath   = APIPrefix + "/pull/agents"
	PullRunTurnsPath = APIPrefix + "/pull/run/turns"
	PullRunTurnPath  = APIPrefix + "/pull/run/turns/{id}"
	PullRunReposPath = APIPrefix + "/pull/run/repos"
	PullRepoPath     = APIPrefix + "/pull/repos/{id}"
)

// PullAgentHandler serves the pull routes over one broker.
type PullAgentHandler struct {
	mode   schemas.Mode
	broker *pullagent.Broker
	signer *pullagent.Signer
	hosted bool
	origin string // origin of signed repository links; "" uses the request's
	write  lib.Middleware
}

// NewPullAgentHandler serves broker with signer. hosted reads the caller's
// org from its principal; local mode has one org (""). origin is the public
// origin of repository links (lib.Config.PullPublicOrigin). write guards the
// agent-side writes in local mode (the write token); nil in hosted mode,
// where the tenancy policy does.
func NewPullAgentHandler(mode schemas.Mode, broker *pullagent.Broker, signer *pullagent.Signer, hosted bool, origin string, write lib.Middleware) *PullAgentHandler {
	return &PullAgentHandler{mode: mode, broker: broker, signer: signer, hosted: hosted, origin: strings.TrimRight(origin, "/"), write: write}
}

// RegisterRoutes implements the route registrar of both servers: the
// agent side and the run side.
func (h *PullAgentHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	h.RegisterAgentRoutes(r, middlewares...)
	h.RegisterRunRoutes(r, middlewares...)
}

// RegisterAgentRoutes registers what the agent's own process calls
// (next, answer, agents): org-scoped routes, also served under /api/v1.
func (h *PullAgentHandler) RegisterAgentRoutes(r *router.Router, middlewares ...lib.Middleware) {
	w := func(f fasthttp.RequestHandler) fasthttp.RequestHandler {
		if h.write != nil {
			f = h.write(f)
		}
		return lib.ChainMiddlewares(f, middlewares...)
	}
	r.POST(PullNextPath, w(h.next))
	r.POST(PullAnswerPath, lib.ChainMiddlewares(h.writeOnly(h.answer), lib.BodyLimit(h.mode, pullagent.MaxAnswerBytes+(1<<20))))
	r.GET(PullAgentsPath, lib.ChainMiddlewares(h.agents, middlewares...))
}

// RegisterRunRoutes registers what a run's launcher calls with its run
// token, and the signed repository link.
func (h *PullAgentHandler) RegisterRunRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.POST(PullRunTurnsPath, lib.ChainMiddlewares(h.runSubmit, lib.BodyLimit(h.mode, pullagent.MaxAnswerBytes+(1<<20))))
	r.GET(PullRunTurnPath, lib.ChainMiddlewares(h.runWait, middlewares...))
	r.POST(PullRunReposPath, lib.ChainMiddlewares(h.runRepo, lib.BodyLimit(h.mode, pullagent.MaxRepoBytes)))
	r.GET(PullRepoPath, h.repo)
}

func (h *PullAgentHandler) writeOnly(f fasthttp.RequestHandler) fasthttp.RequestHandler {
	if h.write != nil {
		return h.write(f)
	}
	return f
}

// org is the caller's organization: its principal's in hosted mode.
func (h *PullAgentHandler) org(ctx *fasthttp.RequestCtx) (string, bool) {
	if !h.hosted {
		return "", true
	}
	p := auth.PrincipalFrom(ctx)
	if !p.IsAuthenticated() || p.OrgID == "" || p.Kind == auth.KindRunner {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "use an API token of the organization the agent belongs to")
		return "", false
	}
	return p.OrgID, true
}

func waitOf(ctx *fasthttp.RequestCtx) time.Duration {
	s, err := strconv.Atoi(string(ctx.QueryArgs().Peek("wait")))
	if err != nil || s < 0 {
		return pullagent.MaxWait
	}
	return min(time.Duration(s)*time.Second, pullagent.MaxWait)
}

// PullNextRequest is the body of POST /pull/next.
type PullNextRequest struct {
	Agent  string `json:"agent"`
	Client string `json:"client,omitempty"` // a stable id of the serving process, for the status list
	WaitS  *int   `json:"wait_s,omitempty"`
}

// PullTurnView is a turn as the serving process gets it.
type PullTurnView struct {
	ID       string          `json:"id"`
	Agent    string          `json:"agent"`
	RunID    string          `json:"run_id"`
	Deadline time.Time       `json:"deadline"`
	Payload  json.RawMessage `json:"payload"`
}

func (h *PullAgentHandler) next(ctx *fasthttp.RequestCtx) {
	org, ok := h.org(ctx)
	if !ok {
		return
	}
	var req PullNextRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body: want {\"agent\": \"<name>\"}")
		return
	}
	if req.Agent == "" {
		req.Agent = string(ctx.QueryArgs().Peek("agent"))
	}
	if !validPullName(req.Agent) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "agent: want the pull agent's name (letters, digits, '_', '.', '-')")
		return
	}
	wait := waitOf(ctx)
	if req.WaitS != nil {
		wait = min(time.Duration(max(*req.WaitS, 0))*time.Second, pullagent.MaxWait)
	}
	if len(req.Client) > 128 {
		req.Client = req.Client[:128]
	}
	t, err := h.broker.Next(contextOf(ctx), org, req.Agent, req.Client, wait)
	if err != nil || t == nil {
		ctx.SetStatusCode(fasthttp.StatusNoContent)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, PullTurnView{ID: t.ID, Agent: t.Agent, RunID: t.Run, Deadline: t.Deadline, Payload: t.Payload})
}

// PullAnswerRequest is the body of POST /pull/turns/{id}/answer: the
// agent's answer object (text, tool_calls, patch, usage ...), or an error
// the process hit.
type PullAnswerRequest struct {
	Answer json.RawMessage `json:"answer,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (h *PullAgentHandler) answer(ctx *fasthttp.RequestCtx) {
	org, ok := h.org(ctx)
	if !ok {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	var req PullAnswerRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil || (len(req.Answer) == 0 && req.Error == "") {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body: want {\"answer\": {...}} or {\"error\": \"...\"}")
		return
	}
	var err error
	if req.Error != "" {
		msg := req.Error
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
		err = h.broker.Fail(org, id, msg)
	} else {
		if req.Answer[0] != '{' {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "answer: want a JSON object")
			return
		}
		err = h.broker.Answer(org, id, req.Answer)
	}
	switch {
	case errors.Is(err, pullagent.ErrNoTurn):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no such turn in this organization")
	case errors.Is(err, pullagent.ErrNotLeased):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
	case errors.Is(err, pullagent.ErrTooLarge):
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "the answer is larger than 16 MiB")
	case err != nil:
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
	default:
		ctx.SetStatusCode(fasthttp.StatusNoContent)
	}
}

// PullAgentsResponse is GET /pull/agents.
type PullAgentsResponse struct {
	Agents []pullagent.AgentStatus `json:"agents"`
}

func (h *PullAgentHandler) agents(ctx *fasthttp.RequestCtx) {
	org, ok := h.org(ctx)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, PullAgentsResponse{Agents: h.broker.Agents(org)})
}

// runClaims checks the run's pull token.
func (h *PullAgentHandler) runClaims(ctx *fasthttp.RequestCtx) (pullagent.Claims, bool) {
	raw := string(ctx.Request.Header.Peek("Authorization"))
	tok, ok := strings.CutPrefix(raw, "Bearer ")
	if !ok || tok == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "a run's pull token is required")
		return pullagent.Claims{}, false
	}
	c, err := h.signer.VerifyRun(tok, time.Now())
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, err.Error())
		return pullagent.Claims{}, false
	}
	if h.hosted && c.Org == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized,
			"this run was planned by a worker that does not name its organization: pull agents run on the shared worker")
		return pullagent.Claims{}, false
	}
	return c, true
}

// PullRunTurnRequest is the body of POST /pull/run/turns.
type PullRunTurnRequest struct {
	TimeoutS int             `json:"timeout_s,omitempty"`
	Payload  json.RawMessage `json:"payload"`
}

// PullRunTurnCreated is its answer.
type PullRunTurnCreated struct {
	ID       string    `json:"id"`
	Deadline time.Time `json:"deadline"`
}

func (h *PullAgentHandler) runSubmit(ctx *fasthttp.RequestCtx) {
	c, ok := h.runClaims(ctx)
	if !ok {
		return
	}
	var req PullRunTurnRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil || len(req.Payload) == 0 || req.Payload[0] != '{' {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body: want {\"payload\": {...}}")
		return
	}
	t, err := h.broker.Submit(c, req.Payload, time.Duration(req.TimeoutS)*time.Second)
	switch {
	case errors.Is(err, pullagent.ErrNoPuller):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, pullagent.NoPullerReason(c.Agent))
	case errors.Is(err, pullagent.ErrQueueFull):
		lib.SendError(ctx, h.mode, fasthttp.StatusTooManyRequests, lib.ErrTypeConflict, err.Error())
	case errors.Is(err, pullagent.ErrTooLarge):
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "the turn is larger than 8 MiB")
	case err != nil:
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
	default:
		lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, PullRunTurnCreated{ID: t.ID, Deadline: t.Deadline})
	}
}

func (h *PullAgentHandler) runWait(ctx *fasthttp.RequestCtx) {
	c, ok := h.runClaims(ctx)
	if !ok {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	res, err := h.broker.Wait(contextOf(ctx), c.Org, c.Run, id, waitOf(ctx))
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no such turn for this run")
		return
	}
	status := fasthttp.StatusOK
	if res.State == pullagent.StatePending || res.State == pullagent.StateLeased {
		status = fasthttp.StatusAccepted
	}
	lib.SendJSON(ctx, h.mode, status, res)
}

// PullRepoCreated is the answer of POST /pull/run/repos.
type PullRepoCreated struct {
	URL       string    `json:"url"`
	SHA256    string    `json:"sha256"`
	Bytes     int       `json:"bytes"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *PullAgentHandler) runRepo(ctx *fasthttp.RequestCtx) {
	c, ok := h.runClaims(ctx)
	if !ok {
		return
	}
	body := ctx.PostBody()
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnsupportedMediaType, lib.ErrTypeUnsupportedMedia, "want a gzip tarball")
		return
	}
	exp := time.Unix(c.Exp, 0)
	info, err := h.broker.PutRepo(c, body, exp)
	switch {
	case errors.Is(err, pullagent.ErrTooLarge):
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "the repository tarball is larger than 48 MiB")
		return
	case errors.Is(err, pullagent.ErrRepoQuota):
		lib.SendError(ctx, h.mode, fasthttp.StatusTooManyRequests, lib.ErrTypeConflict, err.Error())
		return
	case err != nil:
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	origin := h.origin
	if origin == "" {
		scheme := "http"
		if ctx.IsTLS() || strings.EqualFold(string(ctx.Request.Header.Peek("X-Forwarded-Proto")), "https") {
			scheme = "https"
		}
		origin = scheme + "://" + string(ctx.Host())
	}
	q := url.Values{"exp": {strconv.FormatInt(c.Exp, 10)}, "sig": {h.signer.RepoSig(info.ID, c.Exp)}}
	link := origin + strings.Replace(PullRepoPath, "{id}", info.ID, 1) + "?" + q.Encode()
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, PullRepoCreated{URL: link, SHA256: info.SHA256, Bytes: info.Bytes, ExpiresAt: exp})
}

func (h *PullAgentHandler) repo(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	args := ctx.QueryArgs()
	if err := h.signer.VerifyRepo(id, string(args.Peek("exp")), string(args.Peek("sig")), time.Now()); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, err.Error())
		return
	}
	data, sha, err := h.broker.Repo(id)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
		return
	}
	ctx.Response.Header.Set("Content-Type", "application/gzip")
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="repo.tar.gz"`)
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-SHA256", sha)
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(data)
}

func validPullName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '_' || c == '.' || c == '-')
		if !ok {
			return false
		}
	}
	return true
}

// contextOf is the request's context, cancelled when the server shuts down.
func contextOf(ctx *fasthttp.RequestCtx) context.Context { return ctx }
