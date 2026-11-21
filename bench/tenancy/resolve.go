package tenancy

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/valyala/fasthttp"
)

// OrgHeader selects the org of a request explicitly; it is checked against
// the caller's membership (or the org's public_read for signed-in
// non-members' reads).
const OrgHeader = "X-Heimdall-Org"

// Directory looks orgs and memberships up. The database area implements it
// (cmd/bifrost-bench adapts bench/db); tests use a map.
type Directory interface {
	// OrgByID returns the org with id, or an error wrapping ErrOrgNotFound.
	OrgByID(ctx context.Context, id string) (Org, error)
	// OrgBySlug returns the org with slug, or an error wrapping ErrOrgNotFound.
	OrgBySlug(ctx context.Context, slug string) (Org, error)
	// MemberRole returns userID's role in orgID, or an error wrapping
	// ErrNotMember.
	MemberRole(ctx context.Context, orgID, userID string) (auth.Role, error)
}

// Lookup errors.
var (
	ErrOrgNotFound = errors.New("organization not found")
	ErrNotMember   = errors.New("not a member of the organization")
)

// FindOrg resolves ref, an org id or slug, through dir: the id first, then
// the slug.
func FindOrg(ctx context.Context, dir Directory, ref string) (Org, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Org{}, ErrOrgNotFound
	}
	if ValidOrgID(ref) == nil {
		o, err := dir.OrgByID(ctx, ref)
		if err == nil {
			return o, nil
		}
		if !errors.Is(err, ErrOrgNotFound) {
			return Org{}, err
		}
	}
	return dir.OrgBySlug(ctx, ref)
}

func notFound(msg string) *Denied {
	return &Denied{Status: http.StatusNotFound, Type: "not_found", Msg: msg}
}

// Resolve picks the target org of a request for rule and returns it with
// the effective principal: a copy of p whose OrgID is the target and whose
// Role is p's role there (empty when p is not a member). Routes that need no
// org return a nil org and p unchanged.
//
// Org selection: a path {org} for OrgFromPath routes; else the
// X-Heimdall-Org header; else the session's current org, the token's org or
// the runner's org. A token or runner can never select another org than its
// own. An org the caller cannot see answers 404, whether it exists or not.
//
// Anonymous callers never resolve an org: a hosted install requires sign-in
// for every org route, so there is no public demo org, and an org's
// public_read setting admits only signed-in users who are not members
// (read-only), never anonymous visitors.
func Resolve(ctx context.Context, dir Directory, rule Rule, p *auth.Principal, pathRef, headerRef string) (*Org, *auth.Principal, error) {
	if p == nil {
		p = auth.Anonymous()
	}
	if rule.Access == AccessShare {
		return resolveShare(ctx, dir, pathRef)
	}
	if rule.Access != AccessOrg && rule.Access != AccessRunner {
		return nil, p, nil
	}
	ref := headerRef
	if rule.From == OrgFromPath {
		ref = pathRef
		if ref == "" {
			return nil, nil, notFound("organization not found")
		}
	}
	eff := *p
	switch p.Kind {
	case auth.KindToken, auth.KindRunner:
		if p.OrgID == "" {
			return nil, nil, forbidden("credential has no organization")
		}
		o, err := dir.OrgByID(ctx, p.OrgID)
		if err != nil {
			return nil, nil, lookupErr(err)
		}
		if ref != "" && ref != o.ID && ref != o.Slug {
			// A token reaches only its own org; another org's name gets
			// the same answer as an org that does not exist.
			return nil, nil, notFound("organization not found")
		}
		return &o, &eff, nil
	case auth.KindUser:
		if ref == "" {
			ref = p.OrgID
		}
		if ref == "" {
			return nil, nil, forbidden("no organization selected: create one or switch to one")
		}
		o, err := FindOrg(ctx, dir, ref)
		if err != nil {
			return nil, nil, lookupErr(err)
		}
		role, err := dir.MemberRole(ctx, o.ID, p.UserID)
		switch {
		case err == nil:
		case errors.Is(err, ErrNotMember):
			if !o.PublicRead {
				return nil, nil, notFound("organization not found")
			}
			role = "" // public reader of a public_read org
		default:
			return nil, nil, err
		}
		eff.OrgID, eff.Role = o.ID, role
		return &o, &eff, nil
	case auth.KindAnonymous, "":
		return nil, nil, unauthorized("sign in required")
	}
	return nil, nil, forbidden("unknown principal kind " + string(p.Kind))
}

// SharePathParam is the path parameter AccessShare routes carry the share
// token in.
const SharePathParam = "token"

// ShareTokenOrg returns the org id a share token names (the part before its
// first dot), without checking the token: the org's own share store does.
func ShareTokenOrg(token string) (string, bool) {
	org, _, ok := strings.Cut(token, ".")
	return org, ok && ValidOrgID(org) == nil
}

// resolveShare resolves an AccessShare route: the org the token names, and
// an anonymous reader in it, whoever the caller is. A token naming no org
// answers 404, as a bad signature does later.
func resolveShare(ctx context.Context, dir Directory, token string) (*Org, *auth.Principal, error) {
	orgID, ok := ShareTokenOrg(token)
	if !ok {
		return nil, nil, notFound("this share link does not exist")
	}
	o, err := dir.OrgByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, ErrOrgNotFound) {
			return nil, nil, notFound("this share link does not exist")
		}
		return nil, nil, err
	}
	eff := auth.Anonymous()
	eff.OrgID = o.ID
	return &o, eff, nil
}

func lookupErr(err error) error {
	if errors.Is(err, ErrOrgNotFound) {
		return notFound("organization not found")
	}
	return err
}

// Request-scoped org: set by the dispatcher, read by platform handlers.

const orgKey = "heimdall.tenancy.org"

type orgBox struct{ o *Org }

// SetOrg attaches the resolved org to a request.
func SetOrg(ctx *fasthttp.RequestCtx, o *Org) { ctx.SetUserValue(orgKey, orgBox{o}) }

// OrgFrom returns the org the dispatcher resolved for this request, if any.
func OrgFrom(ctx *fasthttp.RequestCtx) (*Org, bool) {
	b, ok := ctx.UserValue(orgKey).(orgBox)
	if !ok || b.o == nil {
		return nil, false
	}
	return b.o, true
}
