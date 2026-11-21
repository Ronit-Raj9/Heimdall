package tenancy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type noDir struct{}

func (noDir) OrgByID(context.Context, string) (Org, error)   { return Org{}, ErrOrgNotFound }
func (noDir) OrgBySlug(context.Context, string) (Org, error) { return Org{}, ErrOrgNotFound }
func (noDir) MemberRole(context.Context, string, string) (auth.Role, error) {
	return "", ErrNotMember
}

// TestDispatcherAuditClientIP: audit rows carry the trusted-proxy client
// address the dispatcher resolved, not the proxy's (4-B-08).
func TestDispatcherAuditClientIP(t *testing.T) {
	var got string
	d, err := NewDispatcher(DispatcherConfig{
		Mode: schemas.ModeLive, Dir: noDir{}, Registry: newTestRegistry(t, &countingBuilder{}, Options{}),
		Platform: func(ctx *fasthttp.RequestCtx) {
			got = EntryFor(ctx, "org_a", "x", "", "", nil).IP
		},
		PlatformRoutes: map[string][]string{"GET": {APIPrefix + "/health"}},
		OrgRoutes:      map[string][]string{},
		ClientIP:       func(*fasthttp.RequestCtx) string { return "203.0.113.7" },
	})
	if err != nil {
		t.Fatal(err)
	}
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1}, nil)
	ctx.Request.SetRequestURI(APIPrefix + "/health")
	d.Handle(&ctx)
	if got != "203.0.113.7" {
		t.Fatalf("audit IP %q (status %d %s)", got, ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var bare fasthttp.RequestCtx
	bare.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1}, nil)
	if ip := ClientIP(&bare); ip != "10.0.0.2" {
		t.Fatalf("fallback IP %q", ip)
	}
}

type recordAudit struct{ got []AuditEntry }

func (r *recordAudit) Audit(_ context.Context, e AuditEntry) error {
	r.got = append(r.got, e)
	return nil
}

// TestAuditCreatedID: a create route's generic audit row names the record
// it committed (4-B-09).
func TestAuditCreatedID(t *testing.T) {
	sink := &recordAudit{}
	d := &Dispatcher{c: DispatcherConfig{Audit: sink, Logger: schemas.NoopLogger{}}}
	var ctx fasthttp.RequestCtx
	SetCreated(&ctx, "rel_1")
	d.audit(&ctx, "org_a", "POST", APIPrefix+"/releases")
	var sub fasthttp.RequestCtx
	sub.SetUserValue("id", "e_1")
	SetCreated(&sub, "rv_1")
	d.audit(&sub, "org_a", "POST", APIPrefix+"/experiments/{id}/notes")
	if len(sink.got) != 2 {
		t.Fatalf("rows %+v", sink.got)
	}
	if e := sink.got[0]; e.Action != "releases.create" || e.TargetID != "rel_1" {
		t.Fatalf("collection create row %+v", e)
	}
	if e := sink.got[1]; e.TargetID != "e_1" || e.Details["created_id"] != "rv_1" {
		t.Fatalf("nested create row %+v", e)
	}
}

// platformDispatcher serves GET /orgs/{org}/audit/export from a one-org
// directory where usr_1 is an admin.
func platformDispatcher(t *testing.T, sink AuditSink, limits *RateLimits) *Dispatcher {
	t.Helper()
	d, err := NewDispatcher(DispatcherConfig{
		Mode: schemas.ModeLive, Registry: newTestRegistry(t, &countingBuilder{}, Options{}),
		Dir:            fakeDir{orgs: []Org{{ID: "org_a", Slug: "acme"}}, members: map[string]auth.Role{"org_a/usr_1": auth.RoleAdmin}},
		Platform:       func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(fasthttp.StatusOK) },
		PlatformRoutes: map[string][]string{"GET": {APIPrefix + "/orgs/{org}/audit/export", APIPrefix + "/orgs/{org}/audit/verify"}},
		OrgRoutes:      map[string][]string{},
		Audit:          sink, Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func serveAs(d *Dispatcher, p *auth.Principal, uri string) *fasthttp.RequestCtx {
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1}, nil)
	ctx.Request.SetRequestURI(uri)
	auth.SetPrincipal(&ctx, p)
	d.Handle(&ctx)
	return &ctx
}

// TestDispatcherAuditsSensitiveReads: a successful GET of a sensitive route
// leaves a .read row; other reads leave none (lane X16).
func TestDispatcherAuditsSensitiveReads(t *testing.T) {
	sink := &recordAudit{}
	d := platformDispatcher(t, sink, nil)
	user := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1"}
	if ctx := serveAs(d, user, APIPrefix+"/orgs/acme/audit/export?format=csv"); ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("export status %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	serveAs(d, user, APIPrefix+"/orgs/acme/audit/verify")
	if len(sink.got) != 1 {
		t.Fatalf("rows %+v", sink.got)
	}
	e := sink.got[0]
	if e.Action != "orgs.audit.export.read" || e.OrgID != "org_a" || e.ActorID != "usr_1" || e.Details["query"] != "format=csv" {
		t.Fatalf("read row %+v", e)
	}
	if !SensitiveReads[key("GET", api+"/runs/{id}/trace")] {
		t.Fatal("trace reads are not audited")
	}
	if r, _ := DefaultPolicy().OrgRule("GET", api+"/runs/{id}/trace"); !r.AuditRead {
		t.Fatal("trace rule does not audit reads")
	}
}

// TestDispatcherRateLimits: a credential over its bucket gets 429 with
// Retry-After; another credential is unaffected until the org bucket runs
// out; runners are never limited here (lane X16, audit N18).
func TestDispatcherRateLimits(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limits := NewRateLimits(RateLimitConfig{
		PerCredential: auth.RateLimit{Burst: 2, Every: 10 * time.Second},
		PerOrg:        auth.RateLimit{Burst: 3, Every: 10 * time.Second},
	}, func() time.Time { return now })
	d := platformDispatcher(t, nil, limits)
	user := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1"}
	uri := APIPrefix + "/orgs/acme/audit/verify"
	for i := 0; i < 2; i++ {
		if c := serveAs(d, user, uri).Response.StatusCode(); c != fasthttp.StatusOK {
			t.Fatalf("request %d: %d", i, c)
		}
	}
	ctx := serveAs(d, user, uri)
	if ctx.Response.StatusCode() != fasthttp.StatusTooManyRequests || string(ctx.Response.Header.Peek("Retry-After")) != "10" {
		t.Fatalf("over the credential limit: %d %q %s", ctx.Response.StatusCode(), ctx.Response.Header.Peek("Retry-After"), ctx.Response.Body())
	}
	// A token of the same org: own credential bucket, shared org bucket
	// (3 taken by the user's two allowed requests and this one).
	tok := &auth.Principal{Kind: auth.KindToken, TokenID: "tok_1", OrgID: "org_a", Role: auth.RoleAdmin}
	if c := serveAs(d, tok, uri).Response.StatusCode(); c != fasthttp.StatusOK {
		t.Fatalf("token first request: %d", c)
	}
	if c := serveAs(d, tok, uri).Response.StatusCode(); c != fasthttp.StatusTooManyRequests {
		t.Fatalf("org bucket not shared: %d", c)
	}
	now = now.Add(time.Minute)
	if c := serveAs(d, user, uri).Response.StatusCode(); c != fasthttp.StatusOK {
		t.Fatalf("after refill: %d", c)
	}
	if limits.allow(&fasthttp.RequestCtx{}, schemas.ModeLive, &auth.Principal{Kind: auth.KindRunner, TokenID: "r"}, &Org{ID: "org_a"}) != true {
		t.Fatal("runner limited")
	}
}
