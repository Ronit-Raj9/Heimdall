package tenancy

import (
	"slices"
	"sort"
	"testing"
)

// TestPublicRoutesAreExactlyTheSite pins every route an anonymous caller
// may reach (lane Y08): health, the sign-in flow, the runner installer, and
// the public site (landing data, docs, benchmark catalog, pricing,
// downloads, security.txt, remote MCP with its own token check). A new
// public route must be added here on purpose; everything else answers 401
// to anonymous callers. Share links are their own access kind.
func TestPublicRoutesAreExactlyTheSite(t *testing.T) {
	want := []string{
		"GET /.well-known/security.txt",
		"GET /api/bench/auth/config",
		"GET /api/bench/auth/github/callback",
		"GET /api/bench/auth/github/start",
		"GET /api/bench/auth/invitations/{token}",
		"GET /api/bench/auth/me",
		"GET /api/bench/health",
		"GET /api/bench/public/benchmarks",
		"GET /api/bench/public/docs",
		"GET /api/bench/public/docs/{name}",
		"GET /api/bench/public/downloads",
		"GET /api/bench/public/overview",
		"GET /api/bench/public/pricing",
		"GET /api/bench/runners/install.sh",
		"GET /downloads/{file}",
		"GET /healthz",
		"GET /mcp",
		"GET /readyz",
		"DELETE /mcp",
		"POST /api/bench/auth/invitations/{token}",
		"POST /api/bench/auth/login",
		"POST /api/bench/auth/logout",
		"POST /api/bench/auth/signup",
		"POST /mcp",
		"GET /api/public/pricing",           // Y16: the plans the pricing page shows
		"GET /api/bench/reference/board",    // Y09: the public reference score board
		"POST /api/bench/auth/verify-email", // Y01: the emailed verification token is the credential
		// Public in the table, but the handler checks a credential of its
		// own: a reset token, an org ingest token, the shared runner secret.
		"GET /api/bench/auth/reset/{token}",
		"POST /api/bench/auth/reset/{token}",
		"POST /api/bench/orgs/{org}/inbox/ingest",
		"POST /api/bench/orgs/{org}/feedback",
		"POST /api/bench/billing/webhook",           // Y16: Stripe signature
		"POST /api/bench/pull/run/turns",            // Y11: signed run token
		"GET /api/bench/pull/run/turns/{id}",        // Y11: signed run token
		"POST /api/bench/pull/run/repos",            // Y11: signed run token
		"GET /api/bench/pull/repos/{id}",            // Y11: signed, expiring link
		"POST /api/bench/auth/device/code",          // Z02: starts a device login, rate limited per IP
		"POST /api/bench/auth/device/token",         // Z02: the device code is the credential
		"POST /api/bench/orgs/{org}/otlp/v1/traces", // Z15: org ingest token
		"POST /api/bench/gateway/usage",             // C03: heimdall plugin usage export, HEIMDALL_GATEWAY_USAGE_SECRET bearer
		"GET /api/bench/admin/pool/assignments",
		"POST /api/bench/admin/pool/assignments/{org}/token",
		"GET /api/bench/status",          // Z14: the public status page
		"GET /api/bench/admin/drain",     // Z11: the handler checks the drain token or a server admin
		"POST /api/bench/admin/drain",    // Z11: same
		"GET /api/bench/registry/token",  // Z13: docker basic auth, checked by the handler
		"POST /api/bench/registry/token", // Z13: same
	}
	var got []string
	for k, r := range PlatformPolicy {
		if r.Access == AccessPublic {
			got = append(got, k)
		}
	}
	for k, r := range OrgPolicy {
		if r.Access == AccessPublic {
			t.Errorf("org route %s is public: org data needs a member, a public_read org or a share link", k)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	for _, k := range got {
		if !slices.Contains(want, k) {
			t.Errorf("unexpected public route %s: add it to this list only if anonymous visitors should reach it", k)
		}
	}
	for _, k := range want {
		if !slices.Contains(got, k) {
			t.Errorf("%s is no longer public", k)
		}
	}
}
