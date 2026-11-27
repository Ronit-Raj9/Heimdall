package handlers

import (
	"context"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// Share link rules (UI fix wave D1, decision 7):
//   - a link that never expires (expires_in_days 0) is admin only; the
//     default expiry is 7 days (sharelinks.DefaultExpiryDays);
//   - GET /shares without both kind and target (every link of the
//     organization, or of every item of a kind) is admin only; the list of
//     one item the share dialog reads answers as before;
//   - a viewer never gets a link's token, path or url in a list;
//   - a shared comparison (verdict) names who made the link, by display
//     name only.
//
// A local caller (local mode, the CLI, a handler test with no principal)
// is the one person of the bench and passes every check, as elsewhere.

const (
	shareNeverRefusal = "Only an org owner or admin may make a link that never expires. Pick an expiry of 1 to 365 days."
	shareListRefusal  = "Only an org owner or admin may list every share link of the organization. Name the item with both kind and target."
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

var (
	memberRealNamesMu sync.RWMutex
	memberRealNames   func(ctx context.Context, orgID string) (map[string]string, error)
)

// SetMemberRealNames installs the resolver from user id, and from
// lower-case email, to the name a member chose, for an organization; a
// member with no name is absent (never the email or a part of it, never
// the id). Only the shared comparison page uses it; nil removes it.
func SetMemberRealNames(f func(ctx context.Context, orgID string) (map[string]string, error)) {
	memberRealNamesMu.Lock()
	defer memberRealNamesMu.Unlock()
	memberRealNames = f
}

// sharedNameMax bounds the creator name on a public page, in bytes.
const sharedNameMax = 120

// sharedCreatorName is the display name of the member who made a link, for
// its visitor: the name the member chose only, never an email, a part of
// one, a login or an id; control and format characters (such as a
// right-to-left override) dropped and spaces folded; "" when there is none
// (local mode, a former member, a member without a name, no resolver) or
// when it is longer than sharedNameMax.
func sharedCreatorName(ctx *fasthttp.RequestCtx, actor string) string {
	actor = strings.TrimSpace(actor)
	o, ok := tenancy.OrgFrom(ctx)
	if actor == "" || !ok || o.ID == "" {
		return ""
	}
	memberRealNamesMu.RLock()
	f := memberRealNames
	memberRealNamesMu.RUnlock()
	if f == nil {
		return ""
	}
	work, cancel := context.WithTimeout(context.Background(), memberNamesTimeout)
	defer cancel()
	names, err := f(work, o.ID)
	if err != nil {
		return ""
	}
	name, ok := names[actor]
	if !ok {
		name = names[strings.ToLower(actor)]
	}
	return cleanSharedName(name, actor)
}

// cleanSharedName drops control and format runes, folds spaces, and
// refuses what looks like an email or an id.
func cleanSharedName(name, actor string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || strings.Contains(name, "@") || strings.EqualFold(name, actor) || strings.HasPrefix(name, "usr_") || len(name) > sharedNameMax {
		return ""
	}
	return name
}
