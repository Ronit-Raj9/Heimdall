package tenancy

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/auth"
)

// The role policy table: one place that says who may call every hosted
// route (docs/platform-design.md, Tenancy). The dispatcher looks the rule up
// by method and router pattern before any handler runs. A route without an
// entry is denied when it writes; an org-router GET without an entry reads
// as viewer. TestPolicyCoversEveryRoute (server/hosted_test.go) fails when a
// registered route has no entry, so a new route is a policy decision, not a
// default.

// Access says what kind of caller a route admits.
type Access int

// Access levels. The zero value denies.
const (
	// AccessDeny refuses every caller.
	AccessDeny Access = iota
	// AccessPublic admits anyone, signed in or not; no org is resolved. The
	// handler itself decides (login, signup, health).
	AccessPublic
	// AccessSignedIn admits any authenticated user or API token; no org is
	// resolved (who am I, my orgs).
	AccessSignedIn
	// AccessUser admits signed-in users (session principals) only; no org
	// is resolved (create an org, switch org).
	AccessUser
	// AccessOrg admits callers holding at least Min in the target org.
	// Anonymous callers, and users who are not members, may call it only
	// with GET or HEAD, only when Min is viewer, and only on an org with
	// public_read.
	AccessOrg
	// AccessRunner admits only runner principals of the target org (the
	// worker routes). Runners are refused on every other route.
	AccessRunner
)

func (a Access) String() string {
	switch a {
	case AccessPublic:
		return "public"
	case AccessSignedIn:
		return "signed-in"
	case AccessUser:
		return "user"
	case AccessOrg:
		return "org"
	case AccessRunner:
		return "runner"
	}
	return "deny"
}

// OrgSource says where the target org of an AccessOrg or AccessRunner route
// comes from.
type OrgSource int

const (
	// OrgFromPrincipal is the session's current org, the token's org, or the
	// X-Heimdall-Org header (checked against membership).
	OrgFromPrincipal OrgSource = iota
	// OrgFromPath is the {org} path parameter (an org id or slug).
	OrgFromPath
)

// OrgPathParam is the path parameter OrgFromPath routes name the org with.
const OrgPathParam = "org"

// Rule is one entry of the policy table.
type Rule struct {
	Access Access
	Min    auth.Role // AccessOrg: minimum role
	From   OrgSource
	// NoAudit marks a write that is not worth an audit row of its own
	// (a runner heartbeat or upload chunk); the request is still checked.
	NoAudit bool
	// Why documents the decision; shown in the denial for AccessDeny.
	Why string
}

// Rule constructors keep the table one line per route.
func public() Rule                      { return Rule{Access: AccessPublic} }
func signedIn() Rule                    { return Rule{Access: AccessSignedIn} }
func userOnly() Rule                    { return Rule{Access: AccessUser} }
func org(min auth.Role) Rule            { return Rule{Access: AccessOrg, Min: min} }
func pathOrg(min auth.Role) Rule        { return Rule{Access: AccessOrg, Min: min, From: OrgFromPath} }
func runner(audit bool) Rule            { return Rule{Access: AccessRunner, NoAudit: !audit} }
func deny(why string) Rule              { return Rule{Access: AccessDeny, Why: why} }
func key(method, pattern string) string { return method + " " + pattern }

const api = "/api/bench"

var (
	viewer = auth.RoleViewer
	member = auth.RoleMember
	admin  = auth.RoleAdmin
	owner  = auth.RoleOwner
)

// PlatformPolicy covers the routes the hosted server serves directly
// (auth, orgs, members, invitations, tokens, jobs, runners, audit, health).
var PlatformPolicy = map[string]Rule{
	// Health (P7): liveness and readiness are public; preflight checks
	// describe the server's own host and are not served to tenants.
	key("GET", api+"/health"): public(),
	key("GET", "/healthz"):    public(),
	key("GET", "/readyz"):     public(),
	key("GET", api+"/doctor"): deny("preflight checks describe the hosting server, not an org"),

	// Auth (P2): the handlers check credentials themselves.
	key("POST", api+"/auth/signup"):              public(),
	key("POST", api+"/auth/login"):               public(),
	key("POST", api+"/auth/logout"):              public(),
	key("GET", api+"/auth/me"):                   public(),
	key("GET", api+"/auth/config"):               public(),
	key("GET", api+"/auth/github/start"):         public(),
	key("GET", api+"/auth/github/callback"):      public(),
	key("GET", api+"/auth/invitations/{token}"):  public(),
	key("POST", api+"/auth/invitations/{token}"): public(),
	key("POST", api+"/auth/switch-org"):          userOnly(),
	key("POST", api+"/auth/password"):            userOnly(), // Auth area (P2): change password (handlers/auth.go)

	// API tokens (P2): admin of the current org.
	key("GET", api+"/auth/tokens"):              org(admin),
	key("POST", api+"/auth/tokens"):             org(admin),
	key("POST", api+"/auth/tokens/{id}/revoke"): org(admin),

	// Orgs and members (P3, handlers/orgs.go, handlers/members.go).
	key("GET", api+"/orgs"):                                signedIn(),
	key("POST", api+"/orgs"):                               userOnly(),
	key("GET", api+"/orgs/{org}"):                          pathOrg(viewer),
	key("POST", api+"/orgs/{org}/rename"):                  pathOrg(admin),
	key("POST", api+"/orgs/{org}/public-read"):             pathOrg(owner),
	key("GET", api+"/orgs/{org}/members"):                  pathOrg(admin),
	key("POST", api+"/orgs/{org}/members/{user}/role"):     pathOrg(admin),
	key("POST", api+"/orgs/{org}/members/{user}/remove"):   pathOrg(admin),
	key("GET", api+"/orgs/{org}/invitations"):              pathOrg(admin),
	key("POST", api+"/orgs/{org}/invitations"):             pathOrg(admin),
	key("POST", api+"/orgs/{org}/invitations/{id}/revoke"): pathOrg(admin),
	key("POST", api+"/orgs/{org}/leave"):                   pathOrg(viewer), // any member, for themselves (handler); non-members refused
	key("GET", api+"/orgs/{org}/audit"):                    pathOrg(admin),

	// Jobs (P4): reads for viewers, starting and cancelling work for members.
	key("GET", api+"/jobs"):              org(viewer),
	key("POST", api+"/jobs"):             org(member),
	key("GET", api+"/jobs/{id}"):         org(viewer),
	key("GET", api+"/jobs/{id}/logs"):    org(viewer),
	key("POST", api+"/jobs/{id}/cancel"): org(member),

	// Runners and runner tokens (P5): admin of the current org.
	key("GET", api+"/runners"):                     org(admin),
	key("GET", api+"/runners/{id}"):                org(admin),
	key("POST", api+"/runners/{id}/remove"):        org(admin), // Runners area: handlers/runners.go
	key("GET", api+"/runners/tokens"):              org(admin),
	key("POST", api+"/runners/tokens"):             org(admin),
	key("POST", api+"/runners/tokens/{id}/revoke"): org(admin),
	key("GET", api+"/runners/install.sh"):          public(),
}

// OrgPolicy covers the org-scoped bench API: the routes every org's router
// registers (server.Deps handlers). GET = viewer; POST that starts or
// records work = member; worker routes = runner of the org.
var OrgPolicy = map[string]Rule{
	key("GET", api+"/health"):       org(viewer),
	key("GET", api+"/doctor"):       deny("preflight checks describe the hosting server, not an org"),
	key("GET", api+"/catalog"):      org(viewer),
	key("GET", api+"/openapi.json"): org(viewer),

	key("GET", api+"/experiments"):                    org(viewer),
	key("POST", api+"/experiments"):                   org(member),
	key("POST", api+"/experiments/plan"):              org(member),
	key("POST", api+"/experiments/{id}/start"):        org(member),
	key("GET", api+"/experiments/{id}"):               org(viewer),
	key("GET", api+"/experiments/{id}/verdict"):       org(viewer),
	key("GET", api+"/experiments/{id}/diagnostics"):   org(viewer),
	key("GET", api+"/experiments/{id}/grid"):          org(viewer),
	key("GET", api+"/experiments/{id}/events"):        org(viewer),
	key("GET", api+"/experiments/{id}/triage"):        org(viewer),
	key("GET", api+"/experiments/{id}/matrix"):        org(viewer),
	key("GET", api+"/experiments/{id}/reviews/queue"): org(viewer),
	key("GET", api+"/experiments/{id}/reviews"):       org(viewer),
	key("POST", api+"/experiments/{id}/cancel"):       org(member), // Jobs area (handlers/experimentjobs.go)
	key("POST", api+"/experiments/{id}/resume"):       org(member),
	key("POST", api+"/experiments/{id}/regrade"):      org(member),
	key("POST", api+"/estimate"):                      org(member),

	key("GET", api+"/runs/{id}"):                             org(viewer),
	key("GET", api+"/runs/{id}/events"):                      org(viewer),
	key("GET", api+"/runs/{id}/diff"):                        org(viewer),
	key("GET", api+"/runs/{id}/receipts"):                    org(viewer),
	key("GET", api+"/runs/{id}/files/{name}"):                org(viewer),
	key("GET", api+"/runs/{id}/evidence/{name}"):             org(viewer),
	key("GET", api+"/runs/{id}/artifacts/{artifact}/{name}"): org(viewer),
	key("GET", api+"/runs/{id}/otlp"):                        org(viewer),
	key("GET", api+"/runs/{id}/steps"):                       org(viewer),
	key("GET", api+"/runs/{id}/remote"):                      org(viewer),
	key("GET", api+"/runs/{id}/reviews"):                     org(viewer),
	key("GET", api+"/pairs/{id}"):                            org(viewer),

	key("GET", api+"/leaderboard"):            org(viewer),
	key("GET", api+"/clusters"):               org(viewer),
	key("GET", api+"/clusters/{signature}"):   org(viewer),
	key("GET", api+"/trends"):                 org(viewer),
	key("POST", api+"/audit"):                 org(member),
	key("POST", api+"/releases"):              org(member),
	key("GET", api+"/releases"):               org(viewer),
	key("GET", api+"/releases/{id}"):          org(viewer),
	key("GET", api+"/releases/{id}/evidence"): org(viewer),
	key("POST", api+"/reviews"):               org(member),

	key("GET", api+"/suites"):                   org(viewer),
	key("GET", api+"/suites/{id}/tasks/{task}"): org(viewer),
	key("GET", api+"/suites/{id}/health"):       org(viewer),
	key("GET", api+"/agents"):                   org(viewer),
	key("GET", api+"/agents/diff"):              org(viewer),
	key("GET", api+"/agents/diff/versions"):     org(viewer),
	key("GET", api+"/imports"):                  org(viewer),

	// Adoption wave and extensions: read routes are viewer reads. Routes that
	// would make the server run an agent or call out to a URL from the org's
	// settings are refused in hosted mode until they run on runners.
	key("GET", api+"/experiments/{id}/adaptive"):       org(viewer),
	key("GET", api+"/experiments/{id}/cost-explain"):   org(viewer),
	key("GET", api+"/experiments/{id}/export"):         org(viewer),
	key("GET", api+"/experiments/{id}/migration"):      org(viewer),
	key("GET", api+"/experiments/{id}/pareto"):         org(viewer),
	key("GET", api+"/experiments/{id}/research"):       org(viewer),
	key("GET", api+"/experiments/{id}/routing"):        org(viewer),
	key("GET", api+"/experiments/{id}/support"):        org(viewer),
	key("GET", api+"/leaderboard/pareto"):              org(viewer),
	key("GET", api+"/runs/{id}/routing"):               org(viewer),
	key("GET", api+"/runs/{id}/visual/{name}"):         org(viewer),
	key("GET", api+"/runs/{id}/voice"):                 org(viewer),
	key("GET", api+"/runs/{id}/voice/audio/{track}"):   org(viewer),
	key("GET", api+"/suites/{id}/export"):              org(viewer),
	key("GET", api+"/suites/{id}/calibration"):         org(viewer),
	key("GET", api+"/suites/{id}/calibration/labels"):  org(viewer),
	key("POST", api+"/suites/{id}/calibration/labels"): org(member),
	key("GET", api+"/canaries"):                        org(viewer),
	key("GET", api+"/canaries/{name}"):                 org(viewer),
	key("POST", api+"/canaries/{name}/run"):            deny("a canary look runs agents; hosted servers run none (use a runner)"),
	key("GET", api+"/inbox"):                           org(viewer),
	key("GET", api+"/inbox/{id}"):                      org(viewer),
	key("POST", api+"/inbox/{id}/accept"):              org(member),
	key("POST", api+"/inbox/{id}/reject"):              org(member),
	key("POST", api+"/inbox/{id}/snooze"):              org(member),
	key("POST", api+"/inbox/ingest"):                   deny("the inbox webhook is local mode only for now"),
	key("GET", api+"/livescore"):                       org(viewer),
	key("GET", api+"/livescore/sessions"):              org(viewer),
	key("GET", api+"/livescore/compare"):               org(viewer),
	key("POST", api+"/livescore/pull"):                 deny("live scoring pulls call a gateway URL; hosted servers make no such calls yet"),

	// Multi-agent, safety and eval integrity reads.
	key("GET", api+"/experiments/{id}/awareness"):             org(viewer),
	key("GET", api+"/experiments/{id}/compliance"):            org(viewer),
	key("GET", api+"/monitors/certifications"):                org(viewer),
	key("GET", api+"/monitors/certifications/{id}"):           org(viewer),
	key("GET", api+"/monitors/certifications/{id}/report.md"): org(viewer),
	key("GET", api+"/runs/{id}/agents"):                       org(viewer),
	key("GET", api+"/runs/{id}/codescan"):                     org(viewer),
	key("GET", api+"/runs/{id}/services"):                     org(viewer),
	key("GET", api+"/runs/{id}/services/{service}/log"):       org(viewer),

	// Remote workers: the list is a status read; everything else is the
	// runner protocol and admits only runners of the org.
	key("GET", api+"/workers"):                              org(viewer),
	key("GET", api+"/workers/whoami"):                       runner(false), // Runners area: runner status
	key("POST", api+"/workers/register"):                    runner(true),
	key("POST", api+"/workers/lease"):                       runner(false),
	key("GET", api+"/workers/manifests/{exp_id}"):           runner(false),
	key("POST", api+"/workers/leases/{lease_id}/heartbeat"): runner(false),
	key("POST", api+"/workers/leases/{lease_id}/upload"):    runner(false),
	key("POST", api+"/workers/leases/{lease_id}/complete"):  runner(true),
	key("POST", api+"/workers/leases/{lease_id}/release"):   runner(true),
}

// Policy is the table the dispatcher consults.
type Policy struct {
	Platform map[string]Rule
	Org      map[string]Rule
}

// DefaultPolicy is the shipped table.
func DefaultPolicy() *Policy { return &Policy{Platform: PlatformPolicy, Org: OrgPolicy} }

// IsRead reports whether method only reads.
func IsRead(method string) bool { return method == http.MethodGet || method == http.MethodHead }

// PlatformRule returns the rule of a platform route. A route without an
// entry is denied.
func (p *Policy) PlatformRule(method, pattern string) (Rule, bool) {
	r, ok := p.Platform[key(readMethod(method), pattern)]
	if !ok {
		return deny("no policy entry for " + method + " " + pattern), false
	}
	return r, true
}

// OrgRule returns the rule of an org route. A GET without an entry reads as
// viewer of the org; any other method without an entry is denied.
func (p *Policy) OrgRule(method, pattern string) (Rule, bool) {
	r, ok := p.Org[key(readMethod(method), pattern)]
	if ok {
		return r, true
	}
	if IsRead(method) {
		return org(viewer), false
	}
	return deny("no policy entry for " + method + " " + pattern), false
}

// readMethod files HEAD under GET.
func readMethod(m string) string {
	if m == http.MethodHead {
		return http.MethodGet
	}
	return m
}

// Denied is a refused request.
type Denied struct {
	Status int    // 401, 403 or 404
	Type   string // lib error type
	Msg    string
}

func (d *Denied) Error() string { return d.Msg }

func unauthorized(msg string) *Denied {
	return &Denied{Status: http.StatusUnauthorized, Type: "unauthorized", Msg: msg}
}
func forbidden(msg string) *Denied {
	return &Denied{Status: http.StatusForbidden, Type: "forbidden", Msg: msg}
}

// Authorize decides one request. p is the effective principal (resolve.go:
// Role is the caller's role in target, empty when not a member); target is
// the resolved org, nil for routes without one.
func Authorize(rule Rule, method string, p *auth.Principal, target *Org) error {
	if p == nil {
		p = auth.Anonymous()
	}
	if p.Kind == auth.KindLocal {
		// Local mode never reaches the hosted dispatcher; a local principal
		// here is a wiring error, refused rather than trusted.
		return forbidden("local principal on a hosted route")
	}
	switch rule.Access {
	case AccessPublic:
		return nil
	case AccessSignedIn:
		if p.Kind == auth.KindUser || p.Kind == auth.KindToken {
			return nil
		}
		if p.Kind == auth.KindRunner {
			return forbidden("runner tokens may call only the worker routes")
		}
		return unauthorized("sign in required")
	case AccessUser:
		if p.Kind == auth.KindUser {
			return nil
		}
		if p.IsAuthenticated() {
			return forbidden("this route needs a signed-in user, not a token")
		}
		return unauthorized("sign in required")
	case AccessRunner:
		if p.Kind != auth.KindRunner {
			if !p.IsAuthenticated() {
				return unauthorized("runner token required")
			}
			return forbidden("only runners may call the worker routes")
		}
		if target == nil || p.OrgID != target.ID {
			return forbidden("runner token is scoped to another organization")
		}
		return nil
	case AccessOrg:
		if target == nil {
			return forbidden("no organization selected")
		}
		if p.Kind == auth.KindRunner {
			return forbidden("runner tokens may call only the worker routes")
		}
		if p.OrgID != target.ID {
			if !p.IsAuthenticated() {
				return unauthorized("sign in required")
			}
			return forbidden("credential is scoped to another organization")
		}
		if p.Role.Valid() && p.Kind != auth.KindAnonymous {
			if p.Role.AtLeast(rule.Min) {
				return nil
			}
			return forbidden(fmt.Sprintf("role %s may not %s here: %s required", p.Role, strings.ToLower(method), rule.Min))
		}
		// Not a member: public read only.
		if IsRead(method) && rule.Min == viewer && target.PublicRead {
			return nil
		}
		if !p.IsAuthenticated() {
			return unauthorized("sign in required")
		}
		return forbidden("not a member of this organization")
	}
	why := rule.Why
	if why == "" {
		why = "route not allowed"
	}
	return forbidden(why)
}

// Entries lists every table entry, sorted, for tests and docs.
func (p *Policy) Entries() (platform, orgScoped []string) {
	for k := range p.Platform {
		platform = append(platform, k)
	}
	for k := range p.Org {
		orgScoped = append(orgScoped, k)
	}
	sort.Strings(platform)
	sort.Strings(orgScoped)
	return platform, orgScoped
}
