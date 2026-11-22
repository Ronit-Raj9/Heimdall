package tenancy

import (
	"net"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// shareDispatcher serves the share routes and one member route of org_a,
// where usr_1 is a member.
func shareDispatcher(t *testing.T, sink AuditSink, limits *RateLimits, now *time.Time) *Dispatcher {
	t.Helper()
	d, err := NewDispatcher(DispatcherConfig{
		Mode: schemas.ModeLive, Registry: newTestRegistry(t, &countingBuilder{}, Options{}),
		Dir:      fakeDir{orgs: []Org{{ID: "org_a", Slug: "acme"}}, members: map[string]auth.Role{"org_a/usr_1": auth.RoleMember}},
		Platform: func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(fasthttp.StatusOK) },
		OrgRoutes: map[string][]string{"GET": {
			APIPrefix + "/shared/{token}", APIPrefix + "/shared/{token}/badge.svg", APIPrefix + "/shares",
		}},
		Audit: sink, Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	d.now = func() time.Time { return *now }
	return d
}

func serveFrom(d *Dispatcher, p *auth.Principal, ip, uri string) *fasthttp.RequestCtx {
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP(ip), Port: 1}, nil)
	ctx.Request.SetRequestURI(uri)
	ctx.Request.Header.Set(OrgHeader, "acme")
	ctx.Request.Header.SetUserAgent("readme-bot/1")
	auth.SetPrincipal(&ctx, p)
	d.Handle(&ctx)
	return &ctx
}

// TestShareTrafficNeverChargesOrgBucket: a flood on one share link exhausts
// that link's and that address's buckets, never the org's, so members keep
// working (audit B4).
func TestShareTrafficNeverChargesOrgBucket(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limits := NewRateLimits(RateLimitConfig{
		PerCredential: auth.RateLimit{Burst: 100, Every: time.Second},
		PerOrg:        auth.RateLimit{Burst: 3, Every: time.Hour},
		PerShareLink:  auth.RateLimit{Burst: 5, Every: time.Hour},
		PerShareIP:    auth.RateLimit{Burst: 4, Every: time.Hour},
	}, func() time.Time { return now })
	d := shareDispatcher(t, nil, limits, &now)
	anon := auth.Anonymous()
	link := APIPrefix + "/shared/org_a.shr_1.sig"
	for i := 0; i < 4; i++ {
		if c := serveFrom(d, anon, "198.51.100.1", link).Response.StatusCode(); c != fasthttp.StatusOK {
			t.Fatalf("share request %d: %d", i, c)
		}
	}
	ctx := serveFrom(d, anon, "198.51.100.1", link)
	if ctx.Response.StatusCode() != fasthttp.StatusTooManyRequests {
		t.Fatalf("over the address limit: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	// Another address: own IP bucket, shared link bucket (5th read).
	if c := serveFrom(d, anon, "198.51.100.2", link).Response.StatusCode(); c != fasthttp.StatusOK {
		t.Fatalf("second address: %d", c)
	}
	ctx = serveFrom(d, anon, "198.51.100.3", link)
	if ctx.Response.StatusCode() != fasthttp.StatusTooManyRequests || ctx.Response.Header.Peek("Retry-After") == nil {
		t.Fatalf("over the link limit: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	// Another link of the same org is unaffected by the first link's bucket.
	if c := serveFrom(d, anon, "198.51.100.4", APIPrefix+"/shared/org_a.shr_2.sig").Response.StatusCode(); c != fasthttp.StatusOK {
		t.Fatalf("other link: %d", c)
	}
	// The org bucket (3) was never charged: a member still gets 3 reads.
	member := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1"}
	for i := 0; i < 3; i++ {
		if c := serveFrom(d, member, "10.0.0.9", APIPrefix+"/shares").Response.StatusCode(); c != fasthttp.StatusOK {
			t.Fatalf("member request %d after share flood: %d", i, c)
		}
	}
	if c := serveFrom(d, member, "10.0.0.9", APIPrefix+"/shares").Response.StatusCode(); c != fasthttp.StatusTooManyRequests {
		t.Fatalf("org bucket not enforced for members: %d", c)
	}
}

// TestShareViewsAudited: a successful share read leaves one shares.view row
// per link, address and route an hour (audit B9).
func TestShareViewsAudited(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	sink := &recordAudit{}
	d := shareDispatcher(t, sink, nil, &now)
	anon := auth.Anonymous()
	link := APIPrefix + "/shared/org_a.shr_1.sig"
	for i := 0; i < 3; i++ {
		serveFrom(d, anon, "198.51.100.1", link)
	}
	serveFrom(d, anon, "198.51.100.1", link+"/badge.svg")
	serveFrom(d, anon, "198.51.100.2", link)
	if len(sink.got) != 3 {
		t.Fatalf("rows %d %+v", len(sink.got), sink.got)
	}
	e := sink.got[0]
	if e.Action != "shares.view" || e.OrgID != "org_a" || e.TargetType != "share" || e.TargetID != "shr_1" ||
		e.ActorType != "anonymous" || e.IP != "198.51.100.1" || e.Details["kind"] != "view" || e.Details["user_agent"] != "readme-bot/1" {
		t.Fatalf("view row %+v", e)
	}
	if sink.got[1].Details["kind"] != "badge" {
		t.Fatalf("badge row %+v", sink.got[1])
	}
	now = now.Add(61 * time.Minute)
	serveFrom(d, anon, "198.51.100.1", link)
	if len(sink.got) != 4 {
		t.Fatalf("no new row after the window: %d", len(sink.got))
	}
	var v shareViews
	if !v.first("k", now) || v.first("k", now.Add(time.Minute)) || !v.first("k", now.Add(2*time.Hour)) {
		t.Fatal("dedupe window")
	}
}
