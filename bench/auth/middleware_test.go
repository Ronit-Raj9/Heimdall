package auth

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// run sends one request through Middleware and reports the principal the
// next handler saw (nil when the middleware answered itself).
func run(mw func(fasthttp.RequestHandler) fasthttp.RequestHandler, setup func(r *fasthttp.Request)) (*Principal, *fasthttp.RequestCtx) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI("/api/bench/experiments")
	ctx.Request.SetHost("heimdall.example")
	ctx.SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 5555})
	if setup != nil {
		setup(&ctx.Request)
	}
	var seen *Principal
	mw(func(c *fasthttp.RequestCtx) {
		seen = PrincipalFrom(c)
		c.SetStatusCode(fasthttp.StatusOK)
	})(ctx)
	return seen, ctx
}

func post(r *fasthttp.Request) { r.Header.SetMethod(fasthttp.MethodPost) }

func TestMiddlewareLocalModeUnchanged(t *testing.T) {
	mw := Middleware(Config{Hosted: false, ServerMode: schemas.ModeLive}, nil)
	for _, setup := range []func(*fasthttp.Request){
		nil,
		post,
		func(r *fasthttp.Request) { post(r); r.Header.Set("Origin", "https://evil.example") },
		func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer hm_garbage") },
	} {
		p, ctx := run(mw, setup)
		if p == nil || p.Kind != KindLocal || p.OrgID != LocalOrgID || p.Role != RoleOwner || ctx.Response.StatusCode() != 200 {
			t.Fatalf("local principal = %+v, status %d", p, ctx.Response.StatusCode())
		}
	}
}

func TestMiddlewareHosted(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sess := e.signup(t, "ada@example.com")
	owner := e.principal(t, sess)
	tok, err := e.svc.CreateToken(bg, owner, TokenCreateRequest{Name: "ci", Role: RoleMember}, "")
	if err != nil {
		t.Fatal(err)
	}
	mw := Middleware(e.svc.cfg, e.svc)
	cookie := func(r *fasthttp.Request) { r.Header.SetCookie(SessionCookie, sess.Cookie) }
	csrf := func(r *fasthttp.Request) { r.Header.Set(CSRFHeader, sess.CSRF) }
	origin := func(o string) func(*fasthttp.Request) { return func(r *fasthttp.Request) { r.Header.Set("Origin", o) } }
	all := func(fs ...func(*fasthttp.Request)) func(*fasthttp.Request) {
		return func(r *fasthttp.Request) {
			for _, f := range fs {
				f(r)
			}
		}
	}
	type want struct {
		status int
		kind   PrincipalKind
	}
	cases := []struct {
		name  string
		setup func(*fasthttp.Request)
		want  want
	}{
		{"anonymous read", nil, want{200, KindAnonymous}},
		{"anonymous write, no origin (CLI)", post, want{200, KindAnonymous}},
		{"anonymous write, allowed origin", all(post, origin("https://heimdall.example")), want{200, KindAnonymous}},
		{"anonymous write, foreign origin", all(post, origin("https://evil.example")), want{403, ""}},
		{"anonymous write, null origin, no fetch metadata", all(post, origin("null")), want{403, ""}},
		{"cookie read", cookie, want{200, KindUser}},
		{"cookie write without CSRF", all(post, cookie, origin("https://heimdall.example")), want{403, ""}},
		{"cookie write, wrong CSRF", all(post, cookie, origin("https://heimdall.example"), func(r *fasthttp.Request) { r.Header.Set(CSRFHeader, "nope") }), want{403, ""}},
		{"cookie write, CSRF, foreign origin", all(post, cookie, csrf, origin("https://evil.example")), want{403, ""}},
		{"cookie write, CSRF, no origin signal", all(post, cookie, csrf), want{403, ""}},
		{"cookie write, CSRF, allowed origin", all(post, cookie, csrf, origin("https://heimdall.example")), want{200, KindUser}},
		{"cookie write, CSRF, origin with default port", all(post, cookie, csrf, origin("https://HEIMDALL.example:443")), want{200, KindUser}},
		{"cookie write, CSRF, referer only", all(post, cookie, csrf, func(r *fasthttp.Request) { r.Header.Set("Referer", "https://heimdall.example/experiments") }), want{200, KindUser}},
		{"cookie write, CSRF, foreign referer", all(post, cookie, csrf, func(r *fasthttp.Request) { r.Header.Set("Referer", "https://evil.example/x") }), want{403, ""}},
		{"cookie write, CSRF, null origin + same-origin fetch", all(post, cookie, csrf, origin("null"), func(r *fasthttp.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }), want{200, KindUser}},
		{"cookie write, CSRF, null origin + cross-site fetch", all(post, cookie, csrf, origin("null"), func(r *fasthttp.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }), want{403, ""}},
		{"cookie write, CSRF, same-site fetch only", all(post, cookie, csrf, func(r *fasthttp.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }), want{403, ""}},
		{"bearer API token read", func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token) }, want{200, KindToken}},
		{"bearer API token write, no CSRF", all(post, origin("https://evil.example"), func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token) }), want{200, KindToken}},
		{"bearer beats cookie", all(cookie, func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token) }), want{200, KindToken}},
		{"bad API token", func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token+"x") }, want{401, ""}},
		{"bad API token does not fall back to cookie", all(cookie, func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer hm_bad") }), want{401, ""}},
		{"unknown bearer scheme", func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer abcdef") }, want{401, ""}},
		{"runner token without verifier", func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer hmr_x") }, want{401, ""}},
	}
	for _, c := range cases {
		p, ctx := run(mw, c.setup)
		st := ctx.Response.StatusCode()
		if st != c.want.status {
			t.Errorf("%s: status %d, want %d (%s)", c.name, st, c.want.status, ctx.Response.Body())
			continue
		}
		if c.want.status == 200 && (p == nil || p.Kind != c.want.kind) {
			t.Errorf("%s: principal %+v, want kind %s", c.name, p, c.want.kind)
		}
		if st == 401 && len(ctx.Response.Header.Peek("WWW-Authenticate")) == 0 {
			t.Errorf("%s: 401 without WWW-Authenticate", c.name)
		}
	}
	// A token principal carries the token's role and org.
	p, _ := run(mw, func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token) })
	if p.Role != RoleMember || p.OrgID != owner.OrgID {
		t.Fatalf("token principal = %+v", p)
	}
	// Bad tokens of every kind answer the same body.
	_, a := run(mw, func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token+"x") })
	_, b := run(mw, func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer hmr_unknown") })
	if string(a.Response.Body()) != string(b.Response.Body()) {
		t.Fatalf("token errors differ:\n%s\n%s", a.Response.Body(), b.Response.Body())
	}
	// A stale cookie is cleared and the request continues anonymous.
	p, ctx := run(mw, func(r *fasthttp.Request) { r.Header.SetCookie(SessionCookie, "stale-value") })
	if sc := string(ctx.Response.Header.PeekCookie(SessionCookie)); p == nil || p.Kind != KindAnonymous ||
		!(strings.Contains(sc, "max-age=0") || strings.Contains(sc, "expires=")) || !strings.Contains(sc, "HttpOnly") {
		t.Fatalf("stale cookie: principal %+v, set-cookie %q", p, ctx.Response.Header.PeekCookie(SessionCookie))
	}
}

func TestRunnerVerifierHandOff(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	mw := Middleware(e.svc.cfg, e.svc)
	t.Cleanup(func() { RegisterRunnerVerifier(nil) })
	RegisterRunnerVerifier(func(_ context.Context, token string) (*Principal, error) {
		switch token {
		case "hmr_good":
			return &Principal{Kind: KindRunner, OrgID: "org_1", TokenID: "rtk_1", Pool: "gpu"}, nil
		case "hmr_liar":
			return &Principal{Kind: KindUser, OrgID: "org_1", Role: RoleOwner}, nil
		}
		return nil, ErrInvalidToken
	})
	p, ctx := run(mw, func(r *fasthttp.Request) { post(r); r.Header.Set("Authorization", "Bearer hmr_good") })
	if ctx.Response.StatusCode() != 200 || p.Kind != KindRunner || p.Pool != "gpu" || p.ActorType() != "runner" {
		t.Fatalf("runner principal = %+v (%d)", p, ctx.Response.StatusCode())
	}
	for _, tok := range []string{"hmr_liar", "hmr_bad"} {
		_, ctx := run(mw, func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer "+tok) })
		if ctx.Response.StatusCode() != 401 {
			t.Fatalf("%s: status %d", tok, ctx.Response.StatusCode())
		}
	}
}

func TestClientIP(t *testing.T) {
	trusted := []*net.IPNet{{IP: net.ParseIP("10.0.0.0").To4(), Mask: net.CIDRMask(8, 32)}}
	mk := func(peer, xff string) *fasthttp.RequestCtx {
		ctx := &fasthttp.RequestCtx{}
		ctx.SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP(peer), Port: 1})
		if xff != "" {
			ctx.Request.Header.Set("X-Forwarded-For", xff)
		}
		return ctx
	}
	cases := []struct{ peer, xff, want string }{
		{"203.0.113.9", "1.2.3.4", "203.0.113.9"},    // untrusted peer: header ignored
		{"10.0.0.2", "1.2.3.4", "1.2.3.4"},           // trusted proxy
		{"10.0.0.2", "6.6.6.6, 1.2.3.4", "1.2.3.4"},  // right-most untrusted wins, spoofed left part ignored
		{"10.0.0.2", "1.2.3.4, 10.0.0.9", "1.2.3.4"}, // chain of trusted proxies
		{"10.0.0.2", "", "10.0.0.2"},                 // no header
		{"10.0.0.2", "garbage", "10.0.0.2"},          // unparsable
	}
	for _, c := range cases {
		if got := clientIP(mk(c.peer, c.xff), trusted); got != c.want {
			t.Errorf("clientIP(%s, %q) = %s, want %s", c.peer, c.xff, got, c.want)
		}
	}
}

func TestRequireRoleAndWriteGuard(t *testing.T) {
	guard := RequireRole(schemas.ModeLive, RoleMember)
	try := func(p *Principal) int {
		ctx := &fasthttp.RequestCtx{}
		if p != nil {
			SetPrincipal(ctx, p)
		}
		guard(func(c *fasthttp.RequestCtx) { c.SetStatusCode(200) })(ctx)
		return ctx.Response.StatusCode()
	}
	if try(nil) != 401 || try(&Principal{Kind: KindUser, Role: RoleViewer}) != 403 || try(&Principal{Kind: KindToken, Role: RoleMember}) != 200 {
		t.Fatal("RequireRole wrong")
	}
	// Local mode: exactly the write token.
	wg := WriteGuard(Config{ServerMode: schemas.ModeLive, WriteToken: "secret"})
	ctx := &fasthttp.RequestCtx{}
	SetPrincipal(ctx, Local())
	wg(func(c *fasthttp.RequestCtx) { c.SetStatusCode(200) })(ctx)
	if ctx.Response.StatusCode() != 401 {
		t.Fatal("local write guard passed without the write token")
	}
	ctx = &fasthttp.RequestCtx{}
	ctx.Request.Header.Set("Authorization", "Bearer secret")
	wg(func(c *fasthttp.RequestCtx) { c.SetStatusCode(200) })(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatal("local write guard refused the write token")
	}
}
