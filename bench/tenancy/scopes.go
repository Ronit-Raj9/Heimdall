package tenancy

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/auth"
)

// API token scopes (public API v1, docs/api-v1.md). A scope narrows what an
// org API token may do below its role; it never widens it. A token without
// scopes (created before scopes existed) is limited by its role alone.
//
//   - read: every read the token's role allows.
//   - evaluate: read, plus starting, estimating and cancelling evaluations
//     and experiments and registering agent versions (the CI loop).
//   - admin: everything the token's role allows.

// evaluateRoutes are the writes the evaluate scope allows, as policy keys.
var evaluateRoutes = map[string]bool{
	key("POST", APIPrefix+"/evaluations"):             true,
	key("POST", APIPrefix+"/evaluations/estimate"):    true,
	key("POST", APIPrefix+"/evaluations/{id}/cancel"): true,
	key("POST", APIPrefix+"/estimate"):                true,
	key("POST", APIPrefix+"/experiments"):             true,
	key("POST", APIPrefix+"/experiments/plan"):        true,
	key("POST", APIPrefix+"/experiments/{id}/start"):  true,
	key("POST", APIPrefix+"/experiments/{id}/cancel"): true,
	key("POST", APIPrefix+"/experiments/{id}/resume"): true,
	key("POST", APIPrefix+"/agents/{id}/versions"):    true,
	key("POST", APIPrefix+"/agents/{id}/test"):        true, // Y10: a CI check sends one sample task
}

// RequiredScope is the token scope a request on pattern needs: admin for a
// route whose rule needs role admin or more, read for any other read,
// evaluate for the evaluate writes, admin for every other write.
func RequiredScope(method, pattern string, rule Rule) string {
	if rule.Min.Valid() && rule.Min.AtLeast(auth.RoleAdmin) {
		return auth.ScopeAdmin
	}
	if IsRead(method) {
		return auth.ScopeRead
	}
	if evaluateRoutes[key(method, pattern)] {
		return auth.ScopeEvaluate
	}
	return auth.ScopeAdmin
}

// CheckScope refuses a token principal whose scopes do not cover the
// request. Sessions, runners and tokens without scopes pass.
func CheckScope(p *auth.Principal, method, pattern string, rule Rule) error {
	if p == nil || p.Kind != auth.KindToken || len(p.Scopes) == 0 {
		return nil
	}
	need := RequiredScope(method, pattern, rule)
	if auth.ScopesAllow(p.Scopes, need) {
		return nil
	}
	return forbidden(fmt.Sprintf("this API token's scopes (%s) do not allow this request: scope %s required", auth.JoinScopes(p.Scopes), need))
}
