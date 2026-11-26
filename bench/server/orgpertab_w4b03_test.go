package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// Wave 4, organization per tab (group B security review): the hosted chain
// (auth, dispatcher, idempotency, org runtime) serves each request in the
// organization the tab names, only when the caller may open it, and a write
// never takes its organization from the query parameter.

// tabDir holds three orgs: u1 is a member of org1 (acme, the session's org)
// and org2 (beta), not of org3 (gamma).
type tabDir struct{ orgs []tenancy.Org }

func (d tabDir) OrgByID(_ context.Context, id string) (tenancy.Org, error) {
	for _, o := range d.orgs {
		if o.ID == id {
			return o, nil
		}
	}
	return tenancy.Org{}, tenancy.ErrOrgNotFound
}

func (d tabDir) OrgBySlug(_ context.Context, slug string) (tenancy.Org, error) {
	for _, o := range d.orgs {
		if o.Slug == slug {
			return o, nil
		}
	}
	return tenancy.Org{}, tenancy.ErrOrgNotFound
}

func (d tabDir) MemberRole(_ context.Context, orgID, userID string) (auth.Role, error) {
	if userID == "u1" && (orgID == "org1" || orgID == "org2") {
		return auth.RoleMember, nil
	}
	return "", tenancy.ErrNotMember
}

type tabFixture struct {
	handler fasthttp.RequestHandler
	mu      sync.Mutex
	// served lists "<org>|<method>|<body>" for every request an org runtime answered.
	served []string
}

func newTabFixture(t *testing.T) *tabFixture {
	t.Helper()
	f := &tabFixture{}
	a := &replayAuth{revoked: map[string]bool{}, creds: map[string]*auth.Principal{
		"sess-u1":   {Kind: auth.KindUser, UserID: "u1", OrgID: "org1", Role: auth.RoleMember},
		"hm_tok1":   {Kind: auth.KindToken, UserID: "u1", TokenID: "tok1", OrgID: "org1", Role: auth.RoleMember},
		"runner-r1": {Kind: auth.KindRunner, TokenID: "rt1", OrgID: "org1"},
	}}
	cfg := testConfig()
	cfg.Auth = lib.AuthConfig{Mode: lib.AuthModeHosted, PublicURL: "https://heimdall.test"}
	build := func(_ context.Context, org tenancy.Org, _ tenancy.OrgPaths) (*tenancy.OrgRuntime, error) {
		return &tenancy.OrgRuntime{Org: org, Handler: func(ctx *fasthttp.RequestCtx) {
			line := org.ID + "|" + string(ctx.Method()) + "|" + string(ctx.Request.Body())
			f.mu.Lock()
			f.served = append(f.served, line)
			n := len(f.served)
			f.mu.Unlock()
			ctx.SetContentType("application/json")
			ctx.SetBodyString(fmt.Sprintf(`{"org":%q,"n":%d}`, org.ID, n))
		}}, nil
	}
	dir := tabDir{orgs: []tenancy.Org{{ID: "org1", Slug: "acme"}, {ID: "org2", Slug: "beta"}, {ID: "org3", Slug: "gamma"}}}
	reg := tenancy.NewRegistry(tenancy.Layout{Hosted: true, StorageRoot: t.TempDir()}, build, tenancy.Options{})
	s, err := NewHosted(cfg, Hosted{Auth: a.middleware, Directory: dir, Registry: reg, Audit: &countAudit{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeHandlers)
	f.handler = s.Handler()
	return f
}

// do sends one request; header names the org in X-Heimdall-Org ("" for none).
func (f *tabFixture) do(cred, method, uri, header, body string, extra ...string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	ctx.Request.Header.SetHost("heimdall.test")
	if cred != "" {
		ctx.Request.Header.Set("Authorization", "Bearer "+cred)
	}
	if header != "" {
		ctx.Request.Header.Set(tenancy.OrgHeader, header)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		ctx.Request.Header.Set(extra[i], extra[i+1])
	}
	if body != "" {
		ctx.Request.Header.SetContentType("application/json")
		ctx.Request.SetBodyString(body)
	}
	f.handler(ctx)
	return ctx
}

func (f *tabFixture) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.served...)
}

func servedOrg(ctx *fasthttp.RequestCtx) string {
	b := string(ctx.Response.Body())
	for _, o := range []string{"org1", "org2", "org3"} {
		if strings.Contains(b, `"org":"`+o+`"`) {
			return o
		}
	}
	return ""
}

// TestOrgParamOnlyOnReads: heimdall_org selects the org of a GET or HEAD
// only; a POST, PUT, PATCH or DELETE carrying only the parameter acts in the
// session's org, never the parameter's.
func TestOrgParamOnlyOnReads(t *testing.T) {
	f := newTabFixture(t)
	for _, tc := range []struct{ method, path, want string }{
		{"GET", "/api/bench/policies/p1", "org2"},
		{"HEAD", "/api/bench/policies/p1", "org2"},
		{"POST", "/api/bench/evaluations", "org1"},
		{"PUT", "/api/bench/policies/p1", "org1"},
		{"PATCH", "/api/bench/experiments/e1/controls", "org1"},
		{"DELETE", "/api/bench/policies/p1", "org1"},
	} {
		body := ""
		if !tenancy.IsRead(tc.method) && tc.method != "DELETE" {
			body = `{"x":1}`
		}
		before := len(f.lines())
		ctx := f.do("sess-u1", tc.method, tc.path+"?heimdall_org=beta", "", body)
		if ctx.Response.StatusCode() >= 400 {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, ctx.Response.StatusCode(), ctx.Response.Body())
		}
		got := f.lines()
		if len(got) != before+1 || !strings.HasPrefix(got[len(got)-1], tc.want+"|") {
			t.Errorf("%s with only the parameter served %v, want %s", tc.method, got[before:], tc.want)
		}
	}
	// The header still names the org of a write.
	if ctx := f.do("sess-u1", "POST", "/api/bench/evaluations?heimdall_org=acme", "beta", `{"x":2}`); servedOrg(ctx) != "org2" {
		t.Errorf("POST with the header beta served %d %s, want org2", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

// TestOrgRefOfAnotherOrgGetsNothing: a header or parameter naming an org the
// user is not a member of (or one that does not exist) answers 404 and
// reaches no org runtime, so no data of that org is served.
func TestOrgRefOfAnotherOrgGetsNothing(t *testing.T) {
	f := newTabFixture(t)
	for _, tc := range []struct{ name, method, uri, header string }{
		{"header GET", "GET", "/api/bench/policies/p1", "gamma"},
		{"header id GET", "GET", "/api/bench/policies/p1", "org3"},
		{"header POST", "POST", "/api/bench/evaluations", "gamma"},
		{"param GET", "GET", "/api/bench/policies/p1?heimdall_org=gamma", ""},
		{"param HEAD", "HEAD", "/api/bench/policies/p1?heimdall_org=org3", ""},
		{"unknown org", "GET", "/api/bench/policies/p1", "nope"},
	} {
		body := ""
		if tc.method == "POST" {
			body = `{"x":1}`
		}
		ctx := f.do("sess-u1", tc.method, tc.uri, tc.header, body)
		if ctx.Response.StatusCode() != fasthttp.StatusNotFound {
			t.Errorf("%s: %d %s, want 404", tc.name, ctx.Response.StatusCode(), ctx.Response.Body())
		}
		if strings.Contains(string(ctx.Response.Body()), "org3") {
			t.Errorf("%s: answer names the other org: %s", tc.name, ctx.Response.Body())
		}
	}
	if got := f.lines(); len(got) != 0 {
		t.Fatalf("an org runtime answered: %v", got)
	}
}

// TestTokensAndRunnersKeepTheirOrg: an API token or a runner naming another
// org, in the header or the parameter, is refused; it reaches only its own.
func TestTokensAndRunnersKeepTheirOrg(t *testing.T) {
	f := newTabFixture(t)
	for _, c := range []struct{ cred, path string }{{"hm_tok1", "/api/bench/policies/p1"}, {"runner-r1", "/api/bench/workers/whoami"}} {
		for _, tc := range []struct{ query, header string }{
			{"", "beta"},
			{"?heimdall_org=beta", ""},
			{"?heimdall_org=org2", ""},
		} {
			ctx := f.do(c.cred, "GET", c.path+tc.query, tc.header, "")
			if ctx.Response.StatusCode() != fasthttp.StatusNotFound || servedOrg(ctx) != "" {
				t.Errorf("%s %s%s header %q: %d %s, want 404", c.cred, c.path, tc.query, tc.header, ctx.Response.StatusCode(), ctx.Response.Body())
			}
		}
		if ctx := f.do(c.cred, "GET", c.path+"?heimdall_org=acme", "acme", ""); servedOrg(ctx) != "org1" {
			t.Errorf("%s naming its own org: %d %s", c.cred, ctx.Response.StatusCode(), ctx.Response.Body())
		}
	}
	for _, l := range f.lines() {
		if !strings.HasPrefix(l, "org1|") {
			t.Fatalf("a credential reached another org: %v", f.lines())
		}
	}
}

// TestTwoTabsWriteInTheirOwnOrg: one session, two tabs on two orgs (same
// credential, different X-Heimdall-Org), each writes with the same
// Idempotency-Key: each write lands in its tab's org, neither is a replay of
// the other, and a repeat in one tab replays only that tab's answer.
func TestTwoTabsWriteInTheirOwnOrg(t *testing.T) {
	f := newTabFixture(t)
	const key = "tab-key-0123456789abcdef"
	a := f.do("sess-u1", "POST", "/api/bench/evaluations", "acme", `{"tab":"A"}`, "Idempotency-Key", key)
	b := f.do("sess-u1", "POST", "/api/bench/evaluations", "beta", `{"tab":"B"}`, "Idempotency-Key", key)
	if servedOrg(a) != "org1" || servedOrg(b) != "org2" {
		t.Fatalf("tab A %d %s, tab B %d %s", a.Response.StatusCode(), a.Response.Body(), b.Response.StatusCode(), b.Response.Body())
	}
	want := []string{`org1|POST|{"tab":"A"}`, `org2|POST|{"tab":"B"}`}
	if got := f.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes %q, want %q", got, want)
	}
	again := f.do("sess-u1", "POST", "/api/bench/evaluations", "beta", `{"tab":"B"}`, "Idempotency-Key", key)
	if string(again.Response.Body()) != string(b.Response.Body()) || string(again.Response.Header.Peek("Idempotent-Replayed")) != "true" {
		t.Fatalf("repeat in tab B: %d %s replayed=%q, want tab B's answer", again.Response.StatusCode(), again.Response.Body(), again.Response.Header.Peek("Idempotent-Replayed"))
	}
	if n := len(f.lines()); n != 2 {
		t.Fatalf("the repeat ran again: %v", f.lines())
	}
}

// TestShareAndPublicRoutesIgnoreOrgRef: a share link opens the org its token
// names whatever the header or parameter say, and a public route answers
// without resolving either.
func TestShareAndPublicRoutesIgnoreOrgRef(t *testing.T) {
	f := newTabFixture(t)
	for _, tc := range []struct{ uri, header string }{
		{"/api/bench/shared/org1.abc", "beta"},
		{"/api/bench/shared/org1.abc?heimdall_org=beta", ""},
		{"/api/bench/shared/org1.abc", "gamma"},
	} {
		ctx := f.do("sess-u1", "GET", tc.uri, tc.header, "")
		if servedOrg(ctx) != "org1" {
			t.Errorf("share %s header %q: %d %s, want org1", tc.uri, tc.header, ctx.Response.StatusCode(), ctx.Response.Body())
		}
	}
	for _, uri := range []string{"/api/bench/health?heimdall_org=gamma", "/healthz?heimdall_org=gamma"} {
		ctx := f.do("", "GET", uri, "gamma", "")
		if ctx.Response.StatusCode() == fasthttp.StatusNotFound || ctx.Response.StatusCode() == fasthttp.StatusForbidden {
			t.Errorf("public %s with another org named: %d %s", uri, ctx.Response.StatusCode(), ctx.Response.Body())
		}
	}
}
