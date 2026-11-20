package handlers

// Tenancy area (P3): members and invitations of an organization. Admin
// routes (tenancy/policy.go). Invitation acceptance is the auth area's
// (POST /auth/invitations/{token}).

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/tenancy"
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
	// owner (ErrLastOwner). They run g's check inside the same transaction
	// as the change, on the roles current there, and return the role the
	// member held before it.
	SetMemberRole(ctx context.Context, orgID, userID string, role auth.Role, g MemberGuard) (MemberInfo, auth.Role, error)
	RemoveMember(ctx context.Context, orgID, userID string, g MemberGuard) (auth.Role, error)
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

// MemberGuard authorizes a membership change against the roles committed
// when it applies, not a snapshot read before it. ActorID is the acting
// user (empty for none); Check gets that user's current role (empty when
// there is no actor or it is no longer a member) and the target's current
// role. A nil Check allows the change. Audit, when set, is the change's
// audit row given the member's previous role; the store appends it in the
// same transaction, so the change never commits without its record.
type MemberGuard struct {
	ActorID string
	Check   func(actor, target auth.Role) error
	Audit   func(from auth.Role) tenancy.AuditEntry
}

// auditIn makes g write the audit row of action in the change's
// transaction when the handler has an audit sink.
func (h *OrgsHandler) auditIn(ctx *fasthttp.RequestCtx, g MemberGuard, orgID, action, userID string, details func(from auth.Role) map[string]any) MemberGuard {
	if h.audit != nil {
		g.Audit = func(from auth.Role) tenancy.AuditEntry {
			return tenancy.EntryFor(ctx, orgID, action, "user", userID, details(from))
		}
	}
	return g
}

// ErrMemberForbidden is a membership change the caller's role does not
// allow once checked against the committed roles (403).
var ErrMemberForbidden = errors.New("role may not make this membership change")

// manageGuard checks mayManage under the membership lock. The caller's role
// is the lower of its principal role and, for a user, its current
// membership role, so a demotion committed meanwhile counts.
func manageGuard(p *auth.Principal, to string) MemberGuard {
	g := MemberGuard{Check: func(actor, target auth.Role) error {
		caller := p.Role
		if p.UserID != "" && (actor.Rank() == 0 || actor.Rank() < caller.Rank()) {
			caller = actor
		}
		if !mayManage(caller, string(target), to) {
			return fmt.Errorf("%w: role %q may not change a %s", ErrMemberForbidden, string(caller), string(target))
		}
		return nil
	}}
	if p.UserID != "" {
		g.ActorID = p.UserID
	}
	return g
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
	g := h.auditIn(ctx, manageGuard(auth.PrincipalFrom(ctx), string(role)), o.ID, "member.role", userID, func(from auth.Role) map[string]any {
		return map[string]any{"from": string(from), "to": string(role)}
	})
	m, _, err := h.orgs.SetMemberRole(wctx, o.ID, userID, role, g)
	if err != nil {
		h.sendErr(ctx, "change role", err)
		return
	}
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
	g := h.auditIn(ctx, manageGuard(auth.PrincipalFrom(ctx), ""), o.ID, "member.remove", userID, func(from auth.Role) map[string]any {
		return map[string]any{"role": string(from)}
	})
	if _, err := h.orgs.RemoveMember(wctx, o.ID, userID, g); err != nil {
		h.sendErr(ctx, "remove member", err)
		return
	}
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
	inv, err := h.orgs.CreateInvitation(wctx, p, o.ID, email, role, tenancy.ClientIP(ctx))
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
	if err := h.orgs.RevokeInvitation(wctx, auth.PrincipalFrom(ctx), o.ID, id, tenancy.ClientIP(ctx)); err != nil {
		h.sendErr(ctx, "revoke invitation", err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}
