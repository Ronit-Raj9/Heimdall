// Package auth resolves every request of the bench API to a Principal: who
// is calling (a user's session, an API token, a runner token, an anonymous
// visitor, or the single-user local mode) and with which role in which
// organization. It also owns passwords, sessions, CSRF, API tokens,
// invitations and GitHub sign-in for the hosted platform
// (docs/platform-design.md, docs/auth.md).
package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/valyala/fasthttp"
)

// PrincipalKind says how a request authenticated.
type PrincipalKind string

// Principal kinds.
const (
	// KindUser is a signed-in user, authenticated by a session cookie.
	KindUser PrincipalKind = "user"
	// KindToken is an API token (hm_...) sent as a bearer token.
	KindToken PrincipalKind = "token"
	// KindRunner is a runner token (hmr_...) verified by the runners area.
	KindRunner PrincipalKind = "runner"
	// KindAnonymous is a request with no valid credential.
	KindAnonymous PrincipalKind = "anonymous"
	// KindLocal is the single-user local mode (auth.mode: local).
	KindLocal PrincipalKind = "local"
)

// LocalOrgID is the one implicit organization of local mode.
const LocalOrgID = "local"

// Role is a membership role, stored as its string in memberships.role,
// api_tokens.role and invitations.role.
type Role string

// Roles, from least to most privileged.
const (
	RoleViewer Role = "viewer"
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
	RoleOwner  Role = "owner"
)

// Roles lists every valid role in ascending order of privilege.
var Roles = []Role{RoleViewer, RoleMember, RoleAdmin, RoleOwner}

// Rank orders roles: viewer 1 < member 2 < admin 3 < owner 4. An unknown or
// empty role ranks 0, below every real role.
func (r Role) Rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleMember:
		return 2
	case RoleAdmin:
		return 3
	case RoleOwner:
		return 4
	}
	return 0
}

// Valid reports whether r is one of the four roles.
func (r Role) Valid() bool { return r.Rank() > 0 }

// AtLeast reports whether r grants everything min grants. It is false when
// either role is unknown, so a typo in a policy table denies instead of
// allowing.
func (r Role) AtLeast(min Role) bool {
	return r.Valid() && min.Valid() && r.Rank() >= min.Rank()
}

// String returns the role as stored.
func (r Role) String() string { return string(r) }

// ParseRole parses a role name, case-insensitively.
func ParseRole(s string) (Role, error) {
	r := Role(strings.ToLower(strings.TrimSpace(s)))
	if !r.Valid() {
		return "", fmt.Errorf("unknown role %q: want viewer, member, admin or owner", s)
	}
	return r, nil
}

// Principal is the caller of one request, set on the request context by the
// auth middleware before any handler runs and read with PrincipalFrom.
type Principal struct {
	Kind PrincipalKind `json:"kind"`
	// UserID is the user behind a session or a user-created API token; empty
	// for runners, anonymous and local principals, and for tokens whose
	// creator was removed.
	UserID string `json:"user_id,omitempty"`
	// OrgID is the organization the request acts in: the session's current
	// organization, the token's organization, or "local".
	OrgID string `json:"org_id,omitempty"`
	// TokenID is the api_tokens or runner_tokens row for token principals.
	TokenID string `json:"token_id,omitempty"`
	// Role is the caller's role in OrgID. For a token it is the token's own
	// role, which never exceeds its creator's role when it was created.
	Role Role `json:"role,omitempty"`
	// Email is the signed-in user's email, for display and audit.
	Email string `json:"email,omitempty"`
	// Pool is the runner pool a runner token is scoped to (runners area).
	Pool string `json:"pool,omitempty"`
	// Scopes narrow an API token below its role (read, evaluate, admin;
	// scopes.go). Empty for every other principal and for tokens made
	// before scopes existed, which their role alone limits.
	Scopes []string `json:"scopes,omitempty"`

	// sessionID is the sha256 (hex) of the session cookie, set only for
	// session principals; logout and switch-org use it.
	sessionID string
	// csrf is the session's CSRF value, set only for session principals.
	csrf string
}

// Anonymous returns a principal with no identity and no role.
func Anonymous() *Principal { return &Principal{Kind: KindAnonymous} }

// Local returns the local-mode principal: organization "local", role owner.
func Local() *Principal { return &Principal{Kind: KindLocal, OrgID: LocalOrgID, Role: RoleOwner} }

// IsAuthenticated reports whether the principal carries a verified
// credential (anything but anonymous). Local mode counts as authenticated.
func (p *Principal) IsAuthenticated() bool {
	return p != nil && p.Kind != "" && p.Kind != KindAnonymous
}

// Can reports whether the principal holds at least min in its organization.
func (p *Principal) Can(min Role) bool {
	return p != nil && p.IsAuthenticated() && p.Role.AtLeast(min)
}

// ActorType is the audit_log.actor_type for this principal: user, token,
// runner, anonymous, or system (local mode).
func (p *Principal) ActorType() string {
	if p == nil {
		return "anonymous"
	}
	switch p.Kind {
	case KindUser:
		return "user"
	case KindToken:
		return "token"
	case KindRunner:
		return "runner"
	case KindLocal:
		return "system"
	}
	return "anonymous"
}

// ActorID is the audit_log.actor_id: the user id, the token id, "local"
// in local mode, or "" for anonymous.
func (p *Principal) ActorID() string {
	if p == nil {
		return ""
	}
	switch p.Kind {
	case KindUser:
		return p.UserID
	case KindToken, KindRunner:
		return p.TokenID
	case KindLocal:
		return string(KindLocal)
	}
	return ""
}

// SessionID returns the hashed session id of a session principal, empty for
// every other kind.
func (p *Principal) SessionID() string {
	if p == nil {
		return ""
	}
	return p.sessionID
}

// principalKey is the context key of the principal. It is a string so that
// fasthttp's RequestCtx.Value (which reads user values) finds it too; the
// value is unexported-typed so nothing outside this package can forge it
// through SetUserValue with another type.
const principalKey = "heimdall.auth.principal"

// WithPrincipal attaches p to ctx. On a *fasthttp.RequestCtx it sets a user
// value and returns the same ctx; on any other context it returns a child
// context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	if rc, ok := ctx.(*fasthttp.RequestCtx); ok {
		rc.SetUserValue(principalKey, principalBox{p})
		return rc
	}
	return context.WithValue(ctx, principalCtxKey{}, principalBox{p})
}

// SetPrincipal attaches p to a fasthttp request.
func SetPrincipal(ctx *fasthttp.RequestCtx, p *Principal) {
	ctx.SetUserValue(principalKey, principalBox{p})
}

// PrincipalFrom returns the principal on ctx, or an anonymous principal when
// none was set: a handler reached without the auth middleware gets no
// privileges, never someone else's. Works on *fasthttp.RequestCtx and on any
// context derived with WithPrincipal.
func PrincipalFrom(ctx context.Context) *Principal {
	if p, ok := Lookup(ctx); ok {
		return p
	}
	return Anonymous()
}

// Lookup returns the principal on ctx and whether one was set.
func Lookup(ctx context.Context) (*Principal, bool) {
	if ctx == nil {
		return nil, false
	}
	var v any
	if rc, ok := ctx.(*fasthttp.RequestCtx); ok {
		v = rc.UserValue(principalKey)
	} else {
		v = ctx.Value(principalCtxKey{})
		if v == nil {
			// A context derived from a RequestCtx reaches its user values.
			v = ctx.Value(principalKey)
		}
	}
	box, ok := v.(principalBox)
	if !ok || box.p == nil {
		return nil, false
	}
	return box.p, true
}

// principalBox wraps the principal so a user value of the same key but
// another type (set by code outside this package) is never mistaken for it.
type principalBox struct{ p *Principal }

// principalCtxKey keys the principal on a plain context.Context.
type principalCtxKey struct{}
