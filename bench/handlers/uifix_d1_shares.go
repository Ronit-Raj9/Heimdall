package handlers

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// Share link rules (UI fix wave D1, decision 7):
//   - a link that never expires (expires_in_days 0) is admin only; the
//     default expiry is 7 days (sharelinks.DefaultExpiryDays);
//   - GET /shares without kind or target (every link of the organization)
//     is admin only; the filtered list the share dialog reads answers as
//     before;
//   - a viewer never gets a link's token, path or url in a list;
//   - a shared comparison (verdict) names who made the link, by display
//     name only.
//
// A local caller (local mode, the CLI, a handler test with no principal)
// is the one person of the bench and passes every check, as elsewhere.

const (
	shareNeverRefusal = "Only an org owner or admin may make a link that never expires. Pick an expiry of 1 to 365 days."
	shareListRefusal  = "Only an org owner or admin may list every share link of the organization. Name the item with kind and target."
)

func shareAdmin(ctx *fasthttp.RequestCtx) bool {
	return isLocalCaller(ctx) || auth.PrincipalFrom(ctx).Can(auth.RoleAdmin)
}

// shareNeverAllowed is false when a non-admin asks for a link that never
// expires.
func shareNeverAllowed(ctx *fasthttp.RequestCtx, req sharelinks.NewShare) bool {
	return req.ExpiresInDays == nil || *req.ExpiresInDays != 0 || shareAdmin(ctx)
}

// shareSecretsAllowed is whether a list may carry each link's token, path
// and url: a member or above (who may make and turn off links), not a
// viewer.
func shareSecretsAllowed(ctx *fasthttp.RequestCtx) bool {
	return isLocalCaller(ctx) || auth.PrincipalFrom(ctx).Can(auth.RoleMember)
}

// sharedCreatorName is the display name of the member who made a link, for
// its visitor: the organization's member name only, never an email or an
// id; "" when there is none (local mode, a former member, no resolver).
func sharedCreatorName(ctx *fasthttp.RequestCtx, actor string) string {
	actor = strings.TrimSpace(actor)
	o, ok := tenancy.OrgFrom(ctx)
	if actor == "" || !ok || o.ID == "" {
		return ""
	}
	memberNamesMu.RLock()
	f := memberNames
	memberNamesMu.RUnlock()
	if f == nil {
		return ""
	}
	work, cancel := context.WithTimeout(context.Background(), memberNamesTimeout)
	defer cancel()
	names, err := f(work, o.ID)
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(names[actor])
	if name == "" {
		name = strings.TrimSpace(names[strings.ToLower(actor)])
	}
	if local, _, isEmail := strings.Cut(name, "@"); isEmail {
		name = local
	}
	if name == "" || name == actor || strings.HasPrefix(name, "usr_") || len(name) > 120 {
		return ""
	}
	return name
}
