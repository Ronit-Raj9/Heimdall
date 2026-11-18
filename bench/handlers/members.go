package handlers

// Tenancy area (P3): members and invitations of an organization. Admin
// routes (tenancy/policy.go). Invitation acceptance is the auth area's
// (POST /auth/invitations/{token}).

import (
	"context"
	"net/mail"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// MemberInfo is one membership.
type MemberInfo struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name,omitempty"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// MemberList is GET /orgs/{org}/members.
type MemberList struct {
	Members []MemberInfo `json:"members"`
}

// RoleRequest is POST /orgs/{org}/members/{user}/role.
type RoleRequest struct {
	Role string `json:"role"`
}

// InvitationRequest is POST /orgs/{org}/invitations.
type InvitationRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// InvitationInfo is one invitation. Token is set only in the answer to the
// create call: it is stored hashed and never shown again.
type InvitationInfo struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	CreatedBy  string     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	Token      string     `json:"token,omitempty"`
	URL        string     `json:"url,omitempty"` // acceptance link, with Token, shown once
}

// InvitationList is GET /orgs/{org}/invitations.
type InvitationList struct {
	Invitations []InvitationInfo `json:"invitations"`
}

// MemberService is the membership and invitation half of OrgService.
type MemberService interface {
	Members(ctx context.Context, orgID string) ([]MemberInfo, error)
	Member(ctx context.Context, orgID, userID string) (MemberInfo, error)
	// SetMemberRole and RemoveMember refuse to leave the org without an
	// owner (ErrLastOwner).
	SetMemberRole(ctx context.Context, orgID, userID string, role auth.Role) (MemberInfo, error)
	RemoveMember(ctx context.Context, orgID, userID string) error
	Invitations(ctx context.Context, orgID string) ([]InvitationInfo, error)
	// CreateInvitation and RevokeInvitation act as inviter (the request's
	// effective principal) and write their own audit rows (auth area).
	CreateInvitation(ctx context.Context, inviter *auth.Principal, orgID, email string, role auth.Role, ip string) (InvitationInfo, error)
	RevokeInvitation(ctx context.Context, p *auth.Principal, orgID, invitationID, ip string) error
}

func (h *OrgsHandler) registerMemberRoutes(r *router.Router, p string, middlewares []lib.Middleware) {
	r.GET(p+"/{org}/members", lib.ChainMiddlewares(h.listMembers, middlewares...))
	r.POST(p+"/{org}/members/{user}/role", lib.ChainMiddlewares(h.setRole, middlewares...))
	r.POST(p+"/{org}/members/{user}/remove", lib.ChainMiddlewares(h.removeMember, middlewares...))
	r.GET(p+"/{org}/invitations", lib.ChainMiddlewares(h.listInvitations, middlewares...))
	r.POST(p+"/{org}/invitations", lib.ChainMiddlewares(h.createInvitation, middlewares...))
	r.POST(p+"/{org}/invitations/{id}/revoke", lib.ChainMiddlewares(h.revokeInvitation, middlewares...))
}

// listMembers handles GET /orgs/{org}/members.
func (h *OrgsHandler) listMembers(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ms, err := h.orgs.Members(wctx, o.ID)
	if err != nil {
		h.sendErr(ctx, "members", err)
		return
	}
	if ms == nil {
		ms = []MemberInfo{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, MemberList{Members: ms})
}

// mayManage reports whether the caller may change a member holding role
// from to role to: the caller must hold at least both. So an admin manages
// members, viewers and other admins; only an owner grants, changes or
// removes ownership.
func mayManage(caller auth.Role, from, to string) bool {
	c := caller.Rank()
	return c > 0 && c >= roleRank(from) && (to == "" || c >= roleRank(to))
}

// setRole handles POST /orgs/{org}/members/{user}/role.
func (h *OrgsHandler) setRole(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	userID, ok := idParam(ctx, h.mode, "user", "member")
	if !ok {
		return
	}
	var req RoleRequest
	if !decodeStrict(ctx, h.mode, &req, "change role") {
		return
	}
	role, err := auth.ParseRole(req.Role)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	cur, err := h.orgs.Member(wctx, o.ID, userID)
	if err != nil {
		h.sendErr(ctx, "member", err)
		return
	}
	caller := auth.PrincipalFrom(ctx).Role
	if !mayManage(caller, cur.Role, string(role)) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"role "+string(caller)+" may not change a "+cur.Role+" to "+string(role))
		return
	}
	m, err := h.orgs.SetMemberRole(wctx, o.ID, userID, role)
	if err != nil {
		h.sendErr(ctx, "change role", err)
		return
	}
	h.write(ctx, o.ID, "member.role", "user", userID, map[string]any{"from": cur.Role, "to": string(role)})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, m)
}

// removeMember handles POST /orgs/{org}/members/{user}/remove.
func (h *OrgsHandler) removeMember(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	userID, ok := idParam(ctx, h.mode, "user", "member")
	if !ok {
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	cur, err := h.orgs.Member(wctx, o.ID, userID)
	if err != nil {
		h.sendErr(ctx, "member", err)
		return
	}
	caller := auth.PrincipalFrom(ctx).Role
	if !mayManage(caller, cur.Role, "") {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "role "+string(caller)+" may not remove a "+cur.Role)
		return
	}
	if err := h.orgs.RemoveMember(wctx, o.ID, userID); err != nil {
		h.sendErr(ctx, "remove member", err)
		return
	}
	h.write(ctx, o.ID, "member.remove", "user", userID, map[string]any{"role": cur.Role})
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// listInvitations handles GET /orgs/{org}/invitations.
func (h *OrgsHandler) listInvitations(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	inv, err := h.orgs.Invitations(wctx, o.ID)
	if err != nil {
		h.sendErr(ctx, "invitations", err)
		return
	}
	if inv == nil {
		inv = []InvitationInfo{}
	}
	for i := range inv {
		inv[i].Token = "" // never listed
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, InvitationList{Invitations: inv})
}

// createInvitation handles POST /orgs/{org}/invitations. The token comes
// back once, in this answer.
func (h *OrgsHandler) createInvitation(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	var req InvitationRequest
	if !decodeStrict(ctx, h.mode, &req, "create invitation") {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "email: a plain address such as ada@example.com")
		return
	}
	role, err := auth.ParseRole(req.Role)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	p := auth.PrincipalFrom(ctx)
	if !mayManage(p.Role, "", string(role)) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "role "+string(p.Role)+" may not invite a "+string(role))
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	inv, err := h.orgs.CreateInvitation(wctx, p, o.ID, email, role, ctx.RemoteIP().String())
	if err != nil {
		h.sendErr(ctx, "create invitation", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, inv)
}

// revokeInvitation handles POST /orgs/{org}/invitations/{id}/revoke.
func (h *OrgsHandler) revokeInvitation(ctx *fasthttp.RequestCtx) {
	o, ok := h.target(ctx)
	if !ok {
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "invitation")
	if !ok {
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := h.orgs.RevokeInvitation(wctx, auth.PrincipalFrom(ctx), o.ID, id, ctx.RemoteIP().String()); err != nil {
		h.sendErr(ctx, "revoke invitation", err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}
