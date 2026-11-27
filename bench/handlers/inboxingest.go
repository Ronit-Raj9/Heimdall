package handlers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/inboxtoken"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// Gap G02: the hosted inbox webhook. Each org keeps its own ingest tokens
// (stored as sha256, shown once); a tool pushes one flagged trace to
// POST /api/bench/orgs/{org}/inbox/ingest with one of them as its bearer,
// and the trace lands in that org's failure inbox exactly as a push to the
// local webhook does (traceinbox.PushBody). These are platform routes of
// the hosted server; local mode keeps POST /api/bench/inbox/ingest.

// InboxTokensPath is the token list of the caller's org.
const InboxTokensPath = APIPrefix + "/inbox/tokens"

// InboxOrgIngestPath is the hosted ingest route; {org} is an org id or slug.
const InboxOrgIngestPath = APIPrefix + "/orgs/{org}/inbox/ingest"

// InboxOrgIngestURL is the ingest path of one org.
func InboxOrgIngestURL(orgID string) string {
	return strings.Replace(InboxOrgIngestPath, "{org}", orgID, 1)
}

// InboxTokenList answers GET /inbox/tokens.
type InboxTokenList struct {
	Tokens []inboxtoken.Token `json:"tokens"`
	// URL is this org's ingest endpoint as the caller reached the server.
	URL string `json:"url"`
	Max int    `json:"max"`
}

// InboxTokenCreateRequest is the body of POST /inbox/tokens.
type InboxTokenCreateRequest struct {
	Label string `json:"label,omitempty"`
}

// InboxTokenCreated answers POST /inbox/tokens: the only time the token
// itself is returned.
type InboxTokenCreated struct {
	Token   inboxtoken.Token `json:"token"`
	Secret  string           `json:"secret"`
	URL     string           `json:"url"`
	Example string           `json:"example"`
}

// InboxOrgInbox opens the failure inbox of one org and the webhook source a
// push is recorded under.
type InboxOrgInbox func(ctx context.Context, org tenancy.Org) (*traceinbox.Inbox, *traceinbox.Source, error)

// InboxTokensHandler serves the hosted inbox tokens and ingest routes.
type InboxTokensHandler struct {
	mode      schemas.Mode
	tokens    inboxtoken.Store
	tokensErr string
	dir       tenancy.Directory
	open      InboxOrgInbox
	now       func() time.Time
	// RatePerMinute and MaxBytes guard each org's pushes.
	RatePerMinute int
	MaxBytes      int64

	mu    sync.Mutex
	gates map[string]*traceinbox.Gate
}

// NewInboxTokensHandler builds the handler. tokens nil answers 503 with
// tokensErr.
func NewInboxTokensHandler(mode schemas.Mode, tokens inboxtoken.Store, tokensErr string, dir tenancy.Directory, open InboxOrgInbox) *InboxTokensHandler {
	return &InboxTokensHandler{mode: mode, tokens: tokens, tokensErr: tokensErr, dir: dir, open: open, now: time.Now,
		RatePerMinute: traceinbox.DefaultRate, MaxBytes: traceinbox.DefaultMaxBody, gates: map[string]*traceinbox.Gate{}}
}

// RegisterRoutes registers the token list, create and revoke and the per-org
// ingest route.
func (h *InboxTokensHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(InboxTokensPath, lib.ChainMiddlewares(h.list, middlewares...))
	r.POST(InboxTokensPath, lib.ChainMiddlewares(h.create, middlewares...))
	r.DELETE(InboxTokensPath+"/{id}", lib.ChainMiddlewares(h.revoke, middlewares...))
	r.POST(InboxOrgIngestPath, lib.ChainMiddlewares(h.ingest, middlewares...))
}

func (h *InboxTokensHandler) ready(ctx *fasthttp.RequestCtx, min auth.Role) (*tenancy.Org, bool) {
	o, ok := tenancy.OrgFrom(ctx)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "request not resolved to an organization")
		return nil, false
	}
	if p := auth.PrincipalFrom(ctx); p == nil || !p.Can(min) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "inbox tokens need the "+string(min)+" role; ask an org admin")
		return nil, false
	}
	if h.tokens == nil {
		msg := "inbox tokens need the server database"
		if h.tokensErr != "" {
			msg += ": " + h.tokensErr
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, msg)
		return nil, false
	}
	return o, true
}

func (h *InboxTokensHandler) list(ctx *fasthttp.RequestCtx) {
	o, ok := h.ready(ctx, auth.RoleViewer)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	l, err := h.tokens.List(work, o.ID)
	if err != nil {
		sendDepError(ctx, h.mode, "inbox tokens", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, InboxTokenList{Tokens: l, URL: orgIngestURL(ctx, o.ID), Max: inboxtoken.MaxPerOrg})
}

func (h *InboxTokensHandler) create(ctx *fasthttp.RequestCtx) {
	o, ok := h.ready(ctx, auth.RoleAdmin)
	if !ok {
		return
	}
	var req InboxTokenCreateRequest
	if len(ctx.PostBody()) > 0 && !(&InboxHandler{mode: h.mode}).decode(ctx, &req) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	t, raw, err := h.tokens.Create(work, o.ID, req.Label, auth.PrincipalFrom(ctx).ActorID())
	switch {
	case errors.Is(err, inboxtoken.ErrInvalid):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	case errors.Is(err, inboxtoken.ErrLimit):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
		return
	case err != nil:
		sendDepError(ctx, h.mode, "create inbox token", err)
		return
	}
	u := orgIngestURL(ctx, o.ID)
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, InboxTokenCreated{Token: *t, Secret: raw, URL: u, Example: ingestExample(u)})
}

func (h *InboxTokensHandler) revoke(ctx *fasthttp.RequestCtx) {
	o, ok := h.ready(ctx, auth.RoleAdmin)
	if !ok {
		return
	}
	id := pathParam(ctx, "id")
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	err := h.tokens.Revoke(work, o.ID, id)
	if errors.Is(err, inboxtoken.ErrNotFound) {
		sendNotFound(ctx, h.mode, "inbox token", id)
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "revoke inbox token", err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// unauthorized is the one answer for an unknown org, a missing, wrong or
// revoked token and another org's token, so a caller learns nothing about
// which orgs exist.
func (h *InboxTokensHandler) unauthorized(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("WWW-Authenticate", `Bearer realm="heimdall-inbox"`)
	lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized,
		"the inbox ingest needs one of this org's inbox tokens: Authorization: Bearer hmi_... (Inbox page, Push failures in, Generate token)")
}

// ingest handles POST /orgs/{org}/inbox/ingest: token, body cap and rate
// limit, in that order, then the push lands in that org's inbox.
func (h *InboxTokensHandler) ingest(ctx *fasthttp.RequestCtx) {
	if h.mode != schemas.ModeLive {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "inbox writes are refused in "+string(h.mode)+" mode")
		return
	}
	if h.tokens == nil || h.dir == nil || h.open == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "the hosted inbox ingest is not configured on this server: it needs the server database")
		return
	}
	raw := strings.TrimSpace(string(ctx.Request.Header.Peek("Authorization")))
	raw, hasBearer := strings.CutPrefix(raw, "Bearer ")
	raw = strings.TrimSpace(raw)
	ref := pathParam(ctx, tenancy.OrgPathParam)
	if !hasBearer || raw == "" || ref == "" {
		h.unauthorized(ctx)
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	org, err := h.dir.OrgByID(work, ref)
	if err != nil {
		org, err = h.dir.OrgBySlug(work, ref)
	}
	if err != nil {
		h.unauthorized(ctx)
		return
	}
	if _, err := h.tokens.Verify(work, org.ID, raw, h.now()); err != nil {
		if !errors.Is(err, inboxtoken.ErrNotFound) {
			sendDepError(ctx, h.mode, "inbox token", err)
			return
		}
		h.unauthorized(ctx)
		return
	}
	if int64(len(ctx.PostBody())) > h.MaxBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "push larger than the ingest limit; push one session")
		return
	}
	if !h.gate(org.ID).Allow(h.now()) {
		ctx.Response.Header.Set("Retry-After", "60")
		lib.SendError(ctx, h.mode, fasthttp.StatusTooManyRequests, lib.ErrTypeUnavailable, "this org's inbox ingest rate limit was reached; retry later")
		return
	}
	ib, src, err := h.open(work, org)
	if err != nil {
		sendDepError(ctx, h.mode, "open the org's inbox", err)
		return
	}
	res, err := traceinbox.PushBody(work, ib, src, ctx.PostBody())
	if err != nil {
		(&InboxHandler{mode: h.mode}).sendErr(ctx, "ingest", err)
		return
	}
	status := fasthttp.StatusOK
	if res.Created {
		status = fasthttp.StatusCreated
	}
	lib.SendJSON(ctx, h.mode, status, res)
}

// gate is the org's rate limiter (only its token bucket is used).
func (h *InboxTokensHandler) gate(orgID string) *traceinbox.Gate {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.gates[orgID]
	if !ok {
		g = traceinbox.NewGate("", h.RatePerMinute, h.MaxBytes)
		h.gates[orgID] = g
	}
	return g
}

// orgIngestURL is the org's hosted ingest endpoint as the caller reached
// this server.
func orgIngestURL(ctx *fasthttp.RequestCtx, orgID string) string {
	return strings.TrimSuffix(ingestURL(ctx), APIPrefix+"/inbox/ingest") + InboxOrgIngestURL(orgID)
}

func ingestExample(url string) string {
	return "curl -X POST " + url + ` -H "Authorization: Bearer $` + DefaultInboxTokenEnv + `" -H "Content-Type: application/json" --data @session.json`
}
