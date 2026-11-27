package handlers

// Tenancy area (P3): organizations of the hosted platform
// (docs/platform-design.md, Tenancy). Platform routes: the dispatcher has
// already resolved {org} to the org the caller may see and checked the role
// policy (tenancy/policy.go) before these handlers run; they read the org
// from tenancy.OrgFrom and the caller from auth.PrincipalFrom. Every write
// leaves one audit row.

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// OrgInfo is one organization as the API returns it.
type OrgInfo struct {
	ID         string    `json:"id"`
	Slug       string    `json:"slug"`
	Name       string    `json:"name"`
	PublicRead bool      `json:"public_read"`
	CreatedAt  time.Time `json:"created_at"`
	// Role is the caller's role in the org, empty for a public reader.
	Role string `json:"role,omitempty"`
}

// OrgList is GET /orgs.
type OrgList struct {
	Orgs []OrgInfo `json:"orgs"`
}

// CreateOrgRequest is POST /orgs.
type CreateOrgRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// RenameOrgRequest is POST /orgs/{org}/rename.
type RenameOrgRequest struct {
	Name string `json:"name"`
}

// PublicReadRequest is POST /orgs/{org}/public-read.
type PublicReadRequest struct {
	PublicRead bool `json:"public_read"`
}

// AuditRow is one audit_log row as the API returns it.
type AuditRow struct {
	ID         string          `json:"id"`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id,omitempty"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type,omitempty"`
	TargetID   string          `json:"target_id,omitempty"`
	IP         string          `json:"ip,omitempty"`
	At         time.Time       `json:"at"`
	Details    json.RawMessage `json:"details,omitempty"`
}

// AuditList is GET /orgs/{org}/audit, newest first.
type AuditList struct {
	Entries []AuditRow `json:"entries"`
	// Next is the opaque cursor of the following page (pass it back as
	// ?cursor=); empty on the last page. Unlike a timestamp it never skips
	// entries that share a millisecond.
	Next string `json:"next,omitempty"`
}

// AuditQuery filters the audit log.
type AuditQuery struct {
	Limit  int       // 1..500, default 100
	Before time.Time // rows strictly older than this; zero: newest
	Cursor string    // AuditList.Next of the previous page, optional
	Action string    // exact action, optional
}

// OrgService is the org, membership and audit store the org and member
// routes use. cmd/bifrost-bench adapts the database area (bench/db) to it;
// errors wrap schemas.ErrNotFound, schemas.ErrExists, ErrOrgInput or
// ErrLastOwner.
type OrgService interface {
	// CreateOrg creates the org with ownerUserID as its owner, and its
	// folders (tenancy.EnsureProject).
	CreateOrg(ctx context.Context, slug, name, ownerUserID string) (OrgInfo, error)
	// OrgsOf lists the orgs userID belongs to, with the role in each.
	OrgsOf(ctx context.Context, userID string) ([]OrgInfo, error)
	Org(ctx context.Context, orgID string) (OrgInfo, error)
	RenameOrg(ctx context.Context, orgID, name string) (OrgInfo, error)
	SetPublicRead(ctx context.Context, orgID string, on bool) (OrgInfo, error)
	MemberService
	// Audit returns one page of the log and the cursor of the next page
	// ("" when it is the last).
	Audit(ctx context.Context, orgID string, q AuditQuery) ([]AuditRow, string, error)
}

// Org and member input errors.
var (
	// ErrOrgInput is a request the store refused as invalid (400).
	ErrOrgInput = errors.New("invalid organization request")
	// ErrLastOwner refuses removing or demoting an org's last owner (409).
	ErrLastOwner = errors.New("an organization must keep at least one owner")
)

// slugPattern is an org slug: lowercase letters, digits and inner hyphens,
// 2 to 40 characters. It never looks like an org id (org_...), which holds
// an underscore.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)

// ValidOrgSlug reports whether slug can name an org.
func ValidOrgSlug(slug string) bool { return slugPattern.MatchString(slug) }

// OrgsHandler serves the org routes.
type OrgsHandler struct {
	mode  schemas.Mode
	orgs  OrgService
	audit tenancy.AuditSink
}

// NewOrgsHandler creates the org handler; orgs nil answers 503.
func NewOrgsHandler(mode schemas.Mode, orgs OrgService, audit tenancy.AuditSink) *OrgsHandler {
	return &OrgsHandler{mode: mode, orgs: orgs, audit: audit}
}

// RegisterRoutes registers the org routes, the member routes and the
// invitation routes (members.go).
func (h *OrgsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	p := APIPrefix + "/orgs"
	r.GET(p, lib.ChainMiddlewares(h.listOrgs, middlewares...))
	r.POST(p, lib.ChainMiddlewares(h.createOrg, middlewares...))
	r.GET(p+"/{org}", lib.ChainMiddlewares(h.getOrg, middlewares...))
	r.POST(p+"/{org}/rename", lib.ChainMiddlewares(h.renameOrg, middlewares...))
	r.POST(p+"/{org}/public-read", lib.ChainMiddlewares(h.setPublicRead, middlewares...))
	r.GET(p+"/{org}/audit", lib.ChainMiddlewares(h.getAudit, middlewares...))
	r.POST(p+"/{org}/leave", lib.ChainMiddlewares(h.leaveOrg, middlewares...))
	h.registerMemberRoutes(r, p, middlewares)
	h.registerDataRoutes(r, p, middlewares) // lane X16: org deletion, audit export and verify (orgdata.go)
}

// OrgRouteDocs describe the org, member and invitation routes (hosted mode
// only; local mode does not register them).
var OrgRouteDocs = []RouteDoc{
	{Method: "GET", Path: APIPrefix + "/orgs", Tag: "orgs", Summary: "Organizations the caller belongs to", Response: OrgList{}},
	{Method: "POST", Path: APIPrefix + "/orgs", Tag: "orgs", Summary: "Create an organization; the caller becomes its owner", Request: CreateOrgRequest{}, Status: fasthttp.StatusCreated, Response: OrgInfo{}},
	{Method: "GET", Path: APIPrefix + "/orgs/{org}", Tag: "orgs", Summary: "One organization", Response: OrgInfo{}},
	{Method: "POST", Path: APIPrefix + "/orgs/{org}/rename", Tag: "orgs", Summary: "Rename an organization (admin)", Request: RenameOrgRequest{}, Response: OrgInfo{}},
	{Method: "POST", Path: APIPrefix + "/orgs/{org}/public-read", Tag: "orgs", Summary: "Let signed-in users who are not members read the organization (owner); anonymous visitors never can", Request: PublicReadRequest{}, Response: OrgInfo{}},
	{Method: "GET", Path: APIPrefix + "/orgs/{org}/audit", Tag: "orgs", Summary: "Audit log, newest first (admin)", Response: AuditList{}, Query: []QueryDoc{
		{Name: "cursor", Description: "Page: next of the previous page"},
		{Name: "q", Description: "Only rows whose actor, action, target, address or details contain this text, any case; reads at most 20 pages from cursor, next continues the search"}}},
	{Method: "POST", Path: APIPrefix + "/orgs/{org}/leave", Tag: "orgs", Summary: "Leave an organization (any member, for themselves; never the last owner)", Status: fasthttp.StatusNoContent},
	{Method: "GET", Path: APIPrefix + "/orgs/{org}/members", Tag: "orgs", Summary: "Members and roles (admin)", Response: MemberList{}},
	{Method: "POST", Path: APIPrefix + "/orgs/{org}/members/{user}/role", Tag: "orgs", Summary: "Change a member's role (admin; owner for owners)", Request: RoleRequest{}, Response: MemberInfo{}},
	{Method: "POST", Path: APIPrefix + "/orgs/{org}/members/{user}/remove", Tag: "orgs", Summary: "Remove a member (admin; never the last owner)", Status: fasthttp.StatusNoContent},
	{Method: "GET", Path: APIPrefix + "/orgs/{org}/invitations", Tag: "orgs", Summary: "Open invitations (admin)", Response: InvitationList{}},
	{Method: "POST", Path: APIPrefix + "/orgs/{org}/invitations", Tag: "orgs", Summary: "Invite by email; the token is shown once (admin)", Request: InvitationRequest{}, Status: fasthttp.StatusCreated, Response: InvitationInfo{}},
	{Method: "POST", Path: APIPrefix + "/orgs/{org}/invitations/{id}/revoke", Tag: "orgs", Summary: "Revoke an invitation (admin)", Status: fasthttp.StatusNoContent},
}

// listOrgs handles GET /orgs: a user's orgs, or a token's one org.
func (h *OrgsHandler) listOrgs(ctx *fasthttp.RequestCtx) {
	if h.orgs == nil {
		sendUnavailable(ctx, h.mode, "organization store")
		return
	}
	p := auth.PrincipalFrom(ctx)
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	var out []OrgInfo
	switch p.Kind {
	case auth.KindUser:
		orgs, err := h.orgs.OrgsOf(wctx, p.UserID)
		if err != nil {
			h.sendErr(ctx, "list organizations", err)
			return
		}
		out = orgs
	case auth.KindToken:
		o, err := h.orgs.Org(wctx, p.OrgID)
		if err != nil {
			h.sendErr(ctx, "organization", err)
			return
		}
		o.Role = string(p.Role)
		out = []OrgInfo{o}
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in required")
		return
	}
	if out == nil {
		out = []OrgInfo{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, OrgList{Orgs: out})
}

// createOrg handles POST /orgs.
func (h *OrgsHandler) createOrg(ctx *fasthttp.RequestCtx) {
	if h.orgs == nil {
		sendUnavailable(ctx, h.mode, "organization store")
		return
	}
	p := auth.PrincipalFrom(ctx)
	if p.Kind != auth.KindUser || p.UserID == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only a signed-in user can create an organization")
		return
	}
	if err := auth.RequireVerified(p); err != nil {
		auth.SendError(ctx, h.mode, err)
		return
	}
	var req CreateOrgRequest
	if !decodeStrict(ctx, h.mode, &req, "create organization") {
		return
	}
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	req.Name = strings.TrimSpace(req.Name)
	if !ValidOrgSlug(req.Slug) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "slug: 2 to 40 lowercase letters, digits or inner hyphens")
		return
	}
	if req.Name == "" {
		req.Name = req.Slug
	}
	if len(req.Name) > 120 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "name: at most 120 characters")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	o, err := h.orgs.CreateOrg(wctx, req.Slug, req.Name, p.UserID)
	if err != nil {
		h.sendErr(ctx, "create organization", err)
		return
	}
	o.Role = string(auth.RoleOwner)
	h.write(ctx, o.ID, "org.create", "org", o.ID, map[string]any{"slug": o.Slug, "name": o.Name})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, o)
}

// getOrg handles GET /orgs/{org}.
func (h *OrgsHandler) getOrg(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	info, err := h.orgs.Org(wctx, o.ID)
	if err != nil {
		h.sendErr(ctx, "organization", err)
		return
	}
	info.Role = string(auth.PrincipalFrom(ctx).Role)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, info)
}

// renameOrg handles POST /orgs/{org}/rename.
func (h *OrgsHandler) renameOrg(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	var req RenameOrgRequest
	if !decodeStrict(ctx, h.mode, &req, "rename organization") {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 120 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "name: 1 to 120 characters")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	info, err := h.orgs.RenameOrg(wctx, o.ID, name)
	if err != nil {
		h.sendErr(ctx, "rename organization", err)
		return
	}
	info.Role = string(auth.PrincipalFrom(ctx).Role)
	h.write(ctx, o.ID, "org.rename", "org", o.ID, map[string]any{"from": o.Name, "to": name})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, info)
}

// setPublicRead handles POST /orgs/{org}/public-read.
func (h *OrgsHandler) setPublicRead(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	var req PublicReadRequest
	if !decodeStrict(ctx, h.mode, &req, "set public read") {
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	info, err := h.orgs.SetPublicRead(wctx, o.ID, req.PublicRead)
	if err != nil {
		h.sendErr(ctx, "set public read", err)
		return
	}
	info.Role = string(auth.PrincipalFrom(ctx).Role)
	h.write(ctx, o.ID, "org.public_read", "org", o.ID, map[string]any{"public_read": req.PublicRead})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, info)
}

// leaveOrg handles POST /orgs/{org}/leave: a signed-in member removes their
// own membership. The last owner is refused (409); an owner hands the org
// over first.
func (h *OrgsHandler) leaveOrg(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	p := auth.PrincipalFrom(ctx)
	if p.Kind != auth.KindUser || p.UserID == "" || !p.Role.Valid() {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only a member, signed in, can leave an organization")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	g := h.auditIn(ctx, MemberGuard{}, o.ID, "member.leave", p.UserID, func(from auth.Role) map[string]any {
		return map[string]any{"role": string(from)}
	})
	if _, err := h.orgs.RemoveMember(wctx, o.ID, p.UserID, g); err != nil {
		h.sendErr(ctx, "leave organization", err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// getAudit handles GET /orgs/{org}/audit?limit=&cursor=&before=<unix ms>&action=.
func (h *OrgsHandler) getAudit(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	q := AuditQuery{Limit: 100, Action: string(ctx.QueryArgs().Peek("action"))}
	if v := ctx.QueryArgs().Peek("limit"); len(v) > 0 {
		n, err := strconv.Atoi(string(v))
		if err != nil || n < 1 || n > 500 {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "limit: 1 to 500")
			return
		}
		q.Limit = n
	}
	if v := ctx.QueryArgs().Peek("before"); len(v) > 0 {
		ms, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil || ms <= 0 {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "before: unix milliseconds")
			return
		}
		q.Before = time.UnixMilli(ms).UTC()
	}
	q.Cursor = string(ctx.QueryArgs().Peek("cursor"))
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	var rows []AuditRow
	var next string
	var err error
	if text := strings.TrimSpace(string(ctx.QueryArgs().Peek("q"))); text != "" {
		rows, next, err = auditSearch(wctx, h.orgs, o.ID, q, text)
	} else {
		rows, next, err = h.orgs.Audit(wctx, o.ID, q)
	}
	if err != nil {
		h.sendErr(ctx, "audit log", err)
		return
	}
	if rows == nil {
		rows = []AuditRow{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AuditList{Entries: rows, Next: next})
}

// target is the org the dispatcher resolved for this request. A request
// that reaches the handler without one was not dispatched: refused.
func (h *OrgsHandler) target(ctx *fasthttp.RequestCtx) (*tenancy.Org, bool) {
	if h.orgs == nil {
		sendUnavailable(ctx, h.mode, "organization store")
		return nil, false
	}
	o, ok := tenancy.OrgFrom(ctx)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "request not resolved to an organization")
		return nil, false
	}
	return o, true
}

// write records one audit row; a failed audit write is logged by the sink
// and does not undo the change.
func (h *OrgsHandler) write(ctx *fasthttp.RequestCtx, orgID, action, targetType, targetID string, details map[string]any) {
	if h.audit == nil {
		return
	}
	actx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	_ = h.audit.Audit(actx, tenancy.EntryFor(ctx, orgID, action, targetType, targetID, details))
}

// sendErr maps a store error.
func (h *OrgsHandler) sendErr(ctx *fasthttp.RequestCtx, what string, err error) {
	if ae, ok := auth.AsError(err); ok {
		lib.SendError(ctx, h.mode, ae.Status, ae.Type, what+": "+ae.Msg)
		return
	}
	switch {
	case errors.Is(err, ErrOrgInput):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, ErrMemberForbidden):
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, what+": "+err.Error())
	case errors.Is(err, ErrLastOwner):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": "+err.Error())
	default:
		sendDepError(ctx, h.mode, what, err)
	}
}

// decodeStrict decodes a JSON body with unknown fields refused, answering
// 400 itself on failure.
func decodeStrict(ctx *fasthttp.RequestCtx, mode schemas.Mode, v any, what string) bool {
	if err := decodeJSONStrict(ctx.PostBody(), v); err != nil {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
		return false
	}
	return true
}

// roleRank compares roles by privilege for the member rules.
func roleRank(r string) int { return auth.Role(r).Rank() }
