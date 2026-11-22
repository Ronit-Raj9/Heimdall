package handlers_test

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"sync"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/valyala/fasthttp"
)

const publicURL = "https://heimdall.example"

// authServer is the auth routes behind the auth middleware, as the hosted
// server mounts them.
type authServer struct {
	h   fasthttp.RequestHandler
	svc *auth.Service
	// mails are the verification mails sent, "to\ntext" each.
	mu    sync.Mutex
	mails []string
}

func newAuthServer(t *testing.T, signup auth.SignupPolicy) *authServer {
	t.Helper()
	cfg := auth.Config{Hosted: true, ServerMode: schemas.ModeLive, PublicURL: publicURL, Signup: signup,
		SessionTTL: 24 * time.Hour, SessionIdle: 24 * time.Hour, InviteTTL: 24 * time.Hour, AllowedOrigins: []string{publicURL}}
	as := &authServer{}
	svc, err := auth.New(cfg, auth.Deps{DB: dbtest.Open(t),
		Hasher: auth.NewHasher(auth.Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}),
		SendMail: func(_ context.Context, to, _, text, _ string) error {
			as.mu.Lock()
			as.mails = append(as.mails, to+"\n"+text)
			as.mu.Unlock()
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	handlers.NewAuthHandler(schemas.ModeLive, svc).RegisterRoutes(r, lib.BodyLimit(schemas.ModeLive, lib.MaxBodyBytes))
	handlers.NewIdentityHandler(schemas.ModeLive, svc).RegisterRoutes(r, lib.BodyLimit(schemas.ModeLive, lib.MaxBodyBytes))
	as.h, as.svc = lib.ChainMiddlewares(r.Handler, auth.Middleware(cfg, svc)), svc
	return as
}

type req struct {
	method, path, body string
	cookie, csrf       string
	origin, bearer     string
	ip                 string
}

func (s *authServer) do(t *testing.T, q req) *fasthttp.RequestCtx {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(q.method)
	ctx.Request.SetRequestURI(q.path)
	ctx.Request.SetHost("heimdall.example")
	ip := q.ip
	if ip == "" {
		ip = "198.51.100.10"
	}
	ctx.SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP(ip), Port: 4000})
	if q.body != "" {
		ctx.Request.Header.SetContentType("application/json")
		ctx.Request.SetBodyString(q.body)
	}
	if q.cookie != "" {
		ctx.Request.Header.SetCookie(auth.SessionCookie, q.cookie)
	}
	if q.csrf != "" {
		ctx.Request.Header.Set(auth.CSRFHeader, q.csrf)
	}
	if q.origin != "" {
		ctx.Request.Header.Set("Origin", q.origin)
	}
	if q.bearer != "" {
		ctx.Request.Header.Set("Authorization", "Bearer "+q.bearer)
	}
	s.h(ctx)
	return ctx
}

func sessionCookie(t *testing.T, ctx *fasthttp.RequestCtx) string {
	t.Helper()
	c := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(c)
	c.SetKey(auth.SessionCookie)
	if !ctx.Response.Header.Cookie(c) {
		t.Fatalf("no session cookie in %s", ctx.Response.Header.String())
	}
	if !c.HTTPOnly() || !c.Secure() || c.SameSite() != fasthttp.CookieSameSiteLaxMode || string(c.Path()) != "/" {
		t.Fatalf("session cookie flags wrong: %s", c.String())
	}
	return string(c.Value())
}

func decode[T any](t *testing.T, ctx *fasthttp.RequestCtx) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(ctx.Response.Body(), &v); err != nil {
		t.Fatalf("decode %s: %v", ctx.Response.Body(), err)
	}
	return v
}

func TestAuthLocalModeRoutes(t *testing.T) {
	cfg := &lib.Config{RunsRoot: t.TempDir(), Server: lib.ServerConfig{Listen: "127.0.0.1:0", Mode: schemas.ModeRecorded}}
	srv, err := server.New(cfg, server.Deps{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	get := func(method, path string) *fasthttp.RequestCtx {
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod(method)
		ctx.Request.SetRequestURI(path)
		ctx.Request.SetHost("127.0.0.1")
		srv.Handler()(ctx)
		return ctx
	}
	me := get("GET", handlers.AuthPrefix+"/me")
	m := decode[auth.Me](t, me)
	if me.Response.StatusCode() != 200 || m.AuthMode != "local" || m.Kind != "local" || m.CurrentOrg != "local" || m.Role != auth.RoleOwner {
		t.Fatalf("local me = %d %s", me.Response.StatusCode(), me.Response.Body())
	}
	if c := get("GET", handlers.AuthPrefix+"/config"); c.Response.StatusCode() != 200 || decode[auth.AuthInfo](t, c).AuthMode != "local" {
		t.Fatalf("local config = %s", c.Response.Body())
	}
	if l := get("POST", handlers.AuthPrefix+"/login"); l.Response.StatusCode() != 404 {
		t.Fatalf("local login = %d", l.Response.StatusCode())
	}
	// The write token still guards writes exactly as before.
	if w := get("POST", handlers.APIPrefix+"/reviews"); w.Response.StatusCode() != 401 {
		t.Fatalf("local write without token = %d", w.Response.StatusCode())
	}
}

func TestAuthHostedFlow(t *testing.T) {
	s := newAuthServer(t, auth.SignupInvite)
	if c := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/config"}); !decode[auth.AuthInfo](t, c).Bootstrap {
		t.Fatalf("config = %s", c.Response.Body())
	}
	// Anonymous me is 401.
	if c := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me"}); c.Response.StatusCode() != 401 {
		t.Fatalf("anonymous me = %d", c.Response.StatusCode())
	}
	// Bootstrap signup.
	sc := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/signup", origin: publicURL,
		body: `{"email":"owner@example.com","password":"violet-tractor-umbrella","name":"Owner","org_name":"Acme"}`})
	if sc.Response.StatusCode() != 201 {
		t.Fatalf("signup = %d %s", sc.Response.StatusCode(), sc.Response.Body())
	}
	cookie := sessionCookie(t, sc)
	me := decode[auth.Me](t, sc)
	if me.CSRF == "" || me.Role != auth.RoleOwner || len(me.Orgs) != 1 || me.Orgs[0].Name != "Acme" {
		t.Fatalf("signup me = %s", sc.Response.Body())
	}
	if strings.Contains(string(sc.Response.Body()), "argon2") {
		t.Fatal("password hash leaked")
	}
	// me with the cookie returns the same CSRF value.
	mc := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me", cookie: cookie})
	if got := decode[auth.Me](t, mc); mc.Response.StatusCode() != 200 || got.CSRF != me.CSRF || got.User.Email != "owner@example.com" {
		t.Fatalf("me = %s", mc.Response.Body())
	}
	// A cookie write without the CSRF header, or from another origin, is refused.
	body := `{"name":"ci","role":"member"}`
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/tokens", cookie: cookie, origin: publicURL, body: body}); c.Response.StatusCode() != 403 {
		t.Fatalf("token without CSRF = %d", c.Response.StatusCode())
	}
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/tokens", cookie: cookie, csrf: me.CSRF, origin: "https://evil.example", body: body}); c.Response.StatusCode() != 403 {
		t.Fatalf("token from foreign origin = %d", c.Response.StatusCode())
	}
	// Unverified: signed in, but no API tokens until the emailed link is
	// opened (Wave A, B1).
	if me.EmailVerified == nil || *me.EmailVerified {
		t.Fatalf("signup me verified: %s", sc.Response.Body())
	}
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/tokens", cookie: cookie, csrf: me.CSRF, origin: publicURL, body: body}); c.Response.StatusCode() != 403 ||
		!strings.Contains(string(c.Response.Body()), "verify your email") {
		t.Fatalf("token by unverified user = %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	resend := s.do(t, req{method: "POST", path: handlers.VerifyResendPath, cookie: cookie, csrf: me.CSRF, origin: publicURL})
	if got := decode[auth.VerifySent](t, resend); resend.Response.StatusCode() != 200 || got.Delivery != auth.DeliveryEmail || strings.Contains(string(resend.Response.Body()), "token=") {
		t.Fatalf("resend = %d %s", resend.Response.StatusCode(), resend.Response.Body())
	}
	s.mu.Lock()
	last := s.mails[len(s.mails)-1]
	s.mu.Unlock()
	m := regexp.MustCompile(`verify-email\?token=(\S+)`).FindStringSubmatch(last)
	if len(m) != 2 {
		t.Fatalf("no link in mail %q", last)
	}
	tok, _ := url.QueryUnescape(m[1])
	if c := s.do(t, req{method: "POST", path: handlers.VerifyEmailPath, origin: publicURL, body: `{"token":"bogus"}`}); c.Response.StatusCode() != 404 {
		t.Fatalf("bogus verify = %d", c.Response.StatusCode())
	}
	vc := s.do(t, req{method: "POST", path: handlers.VerifyEmailPath, origin: publicURL, body: `{"token":"` + tok + `"}`})
	if got := decode[auth.VerifyResult](t, vc); vc.Response.StatusCode() != 200 || !got.Verified || got.Email != "owner@example.com" {
		t.Fatalf("verify = %d %s", vc.Response.StatusCode(), vc.Response.Body())
	}
	if got := decode[auth.Me](t, s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me", cookie: cookie})); got.EmailVerified == nil || !*got.EmailVerified {
		t.Fatal("me not verified after the link")
	}
	// Signup settings are server admins only.
	if c := s.do(t, req{method: "GET", path: handlers.SignupSettingPath, cookie: cookie}); c.Response.StatusCode() != 403 {
		t.Fatalf("signup settings by non-admin = %d", c.Response.StatusCode())
	}
	tc := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/tokens", cookie: cookie, csrf: me.CSRF, origin: publicURL, body: body})
	if tc.Response.StatusCode() != 201 {
		t.Fatalf("token create = %d %s", tc.Response.StatusCode(), tc.Response.Body())
	}
	created := decode[auth.TokenCreated](t, tc)
	// The token authenticates without cookie or CSRF, scoped to its role.
	tm := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me", bearer: created.Token})
	if got := decode[auth.Me](t, tm); got.Kind != "token" || got.Role != auth.RoleMember || got.CSRF != "" {
		t.Fatalf("token me = %s", tm.Response.Body())
	}
	// A member token can not list tokens (admin), nor mint new ones.
	if c := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/tokens", bearer: created.Token}); c.Response.StatusCode() != 403 {
		t.Fatalf("member token list = %d", c.Response.StatusCode())
	}
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/tokens", bearer: created.Token, body: body}); c.Response.StatusCode() != 403 {
		t.Fatalf("token minted a token: %d", c.Response.StatusCode())
	}
	lc := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/tokens", cookie: cookie})
	if list := decode[handlers.TokenList](t, lc); len(list.Tokens) != 1 || strings.Contains(string(lc.Response.Body()), created.Token) {
		t.Fatalf("token list = %s", lc.Response.Body())
	}
	// Revoke, then the token is dead.
	rc := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/tokens/" + created.APIToken.ID + "/revoke", cookie: cookie, csrf: me.CSRF, origin: publicURL})
	if rc.Response.StatusCode() != 204 {
		t.Fatalf("revoke = %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if c := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me", bearer: created.Token}); c.Response.StatusCode() != 401 {
		t.Fatalf("revoked token = %d", c.Response.StatusCode())
	}
	// Login rotates the session. With a live session cookie, login is a
	// cookie write like any other: it needs the CSRF value too.
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/login", cookie: cookie, origin: publicURL,
		body: `{"email":"owner@example.com","password":"violet-tractor-umbrella"}`}); c.Response.StatusCode() != 403 {
		t.Fatalf("login over a session without CSRF = %d", c.Response.StatusCode())
	}
	lg := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/login", cookie: cookie, csrf: me.CSRF, origin: publicURL,
		body: `{"email":"owner@example.com","password":"violet-tractor-umbrella"}`})
	if lg.Response.StatusCode() != 200 {
		t.Fatalf("login = %d %s", lg.Response.StatusCode(), lg.Response.Body())
	}
	cookie2 := sessionCookie(t, lg)
	if cookie2 == cookie {
		t.Fatal("login kept the session id")
	}
	if c := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me", cookie: cookie}); c.Response.StatusCode() != 401 {
		t.Fatalf("old session after login = %d", c.Response.StatusCode())
	}
	csrf2 := decode[auth.Me](t, lg).CSRF
	// Logout ends the session and clears the cookie.
	lo := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/logout", cookie: cookie2, csrf: csrf2, origin: publicURL})
	if lo.Response.StatusCode() != 204 || !strings.Contains(string(lo.Response.Header.PeekCookie(auth.SessionCookie)), "max-age=0") {
		t.Fatalf("logout = %d %s", lo.Response.StatusCode(), lo.Response.Header.PeekCookie(auth.SessionCookie))
	}
	if c := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me", cookie: cookie2}); c.Response.StatusCode() != 401 {
		t.Fatalf("me after logout = %d", c.Response.StatusCode())
	}
}

func TestAuthLoginEnumerationSafeAndRateLimited(t *testing.T) {
	s := newAuthServer(t, auth.SignupOpen)
	s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/signup", body: `{"email":"ada@example.com","password":"violet-tractor-umbrella"}`})
	wrong := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/login", ip: "198.51.100.20", body: `{"email":"ada@example.com","password":"not-the-password"}`})
	unknown := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/login", ip: "198.51.100.21", body: `{"email":"nobody@example.com","password":"not-the-password"}`})
	if wrong.Response.StatusCode() != 401 || unknown.Response.StatusCode() != 401 || string(wrong.Response.Body()) != string(unknown.Response.Body()) {
		t.Fatalf("login failures differ:\n%d %s\n%d %s", wrong.Response.StatusCode(), wrong.Response.Body(), unknown.Response.StatusCode(), unknown.Response.Body())
	}
	// Login CSRF: an anonymous write from a foreign origin is refused.
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/login", origin: "https://evil.example", body: `{"email":"ada@example.com","password":"violet-tractor-umbrella"}`}); c.Response.StatusCode() != 403 {
		t.Fatalf("cross-origin login = %d", c.Response.StatusCode())
	}
	var last *fasthttp.RequestCtx
	for i := 0; i < 40; i++ {
		last = s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/login", ip: "198.51.100.30", body: `{"email":"ada@example.com","password":"guess"}`})
		if last.Response.StatusCode() == 429 {
			break
		}
	}
	if last.Response.StatusCode() != 429 || len(last.Response.Header.Peek("Retry-After")) == 0 {
		t.Fatalf("no 429 after repeated failures: %d", last.Response.StatusCode())
	}
	// Strict bodies.
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/login", ip: "198.51.100.40", body: `{"email":"a@example.com","password":"x","admin":true}`}); c.Response.StatusCode() != 400 {
		t.Fatalf("unknown field = %d", c.Response.StatusCode())
	}
	if c := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/invitations/nope"}); c.Response.StatusCode() != 404 {
		t.Fatalf("unknown invitation = %d", c.Response.StatusCode())
	}
	if c := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/github/start"}); c.Response.StatusCode() != 404 {
		t.Fatalf("github start without app = %d", c.Response.StatusCode())
	}
}

func TestAuthInvitationAcceptAndSwitchOrg(t *testing.T) {
	s := newAuthServer(t, auth.SignupOpen)
	owner := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/signup", body: `{"email":"owner@example.com","password":"violet-tractor-umbrella","org_name":"Acme"}`})
	ownerMe := decode[auth.Me](t, owner)
	guest := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/signup", ip: "198.51.100.11", body: `{"email":"guest@example.com","password":"violet-tractor-umbrella"}`})
	gCookie, gMe := sessionCookie(t, guest), decode[auth.Me](t, guest)
	ownerP := &auth.Principal{Kind: auth.KindUser, UserID: ownerMe.User.ID, OrgID: ownerMe.CurrentOrg, Role: auth.RoleOwner, EmailVerified: true}
	inv, err := s.svc.CreateInvitation(t.Context(), ownerP, ownerMe.CurrentOrg, "guest@example.com", auth.RoleMember, "")
	if err != nil {
		t.Fatal(err)
	}
	info := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/invitations/" + inv.Token})
	if got := decode[auth.InvitationInfo](t, info); got.OrgName != "Acme" || !got.AccountExists {
		t.Fatalf("invitation info = %s", info.Response.Body())
	}
	// Accepting needs the session's CSRF like any cookie write.
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/invitations/" + inv.Token, cookie: gCookie, origin: publicURL}); c.Response.StatusCode() != 403 {
		t.Fatalf("accept without CSRF = %d", c.Response.StatusCode())
	}
	acc := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/invitations/" + inv.Token, cookie: gCookie, csrf: gMe.CSRF, origin: publicURL})
	if got := decode[auth.MeOrg](t, acc); acc.Response.StatusCode() != 200 || got.ID != ownerMe.CurrentOrg || got.Role != auth.RoleMember {
		t.Fatalf("accept = %d %s", acc.Response.StatusCode(), acc.Response.Body())
	}
	mine := decode[auth.Me](t, s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me", cookie: gCookie}))
	if mine.CurrentOrg != ownerMe.CurrentOrg || len(mine.Orgs) != 2 {
		t.Fatalf("guest me after accept = %+v", mine)
	}
	sw := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/switch-org", cookie: gCookie, csrf: gMe.CSRF, origin: publicURL, body: `{"org":"` + gMe.CurrentOrg + `"}`})
	if sw.Response.StatusCode() != 200 {
		t.Fatalf("switch = %d %s", sw.Response.StatusCode(), sw.Response.Body())
	}
	// Password change through the API.
	pw := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/password", cookie: gCookie, csrf: gMe.CSRF, origin: publicURL,
		body: `{"current_password":"violet-tractor-umbrella","new_password":"orange-bicycle-lantern"}`})
	if pw.Response.StatusCode() != 204 {
		t.Fatalf("password = %d %s", pw.Response.StatusCode(), pw.Response.Body())
	}
}

// 4-A-14: a throttled GitHub callback answers 429 with Retry-After, not
// the generic sign-in redirect. 4-A-13: trailing data after the JSON body
// is refused.
func TestAuthCallbackThrottleAndStrictBody(t *testing.T) {
	cfg := auth.Config{Hosted: true, ServerMode: schemas.ModeLive, PublicURL: publicURL, Signup: auth.SignupOpen,
		SessionTTL: 24 * time.Hour, SessionIdle: 24 * time.Hour, InviteTTL: 24 * time.Hour, AllowedOrigins: []string{publicURL},
		GitHub: &auth.GitHubConfig{ClientID: "cid", ClientSecret: "test-only", TokenURL: "http://127.0.0.1:1/token", APIURL: "http://127.0.0.1:1"}}
	svc, err := auth.New(cfg, auth.Deps{DB: dbtest.Open(t),
		Hasher: auth.NewHasher(auth.Argon2Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32})})
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	handlers.NewAuthHandler(schemas.ModeLive, svc).RegisterRoutes(r, lib.BodyLimit(schemas.ModeLive, lib.MaxBodyBytes))
	s := &authServer{h: lib.ChainMiddlewares(r.Handler, auth.Middleware(cfg, svc)), svc: svc}
	var last *fasthttp.RequestCtx
	for i := 0; i < 40; i++ {
		last = s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/github/callback?state=x&code=y", ip: "198.51.100.77"})
		if last.Response.StatusCode() == 429 {
			break
		}
		if last.Response.StatusCode() != 302 {
			t.Fatalf("callback %d = %d", i, last.Response.StatusCode())
		}
	}
	if last.Response.StatusCode() != 429 || len(last.Response.Header.Peek("Retry-After")) == 0 {
		t.Fatalf("throttled callback = %d, Retry-After %q", last.Response.StatusCode(), last.Response.Header.Peek("Retry-After"))
	}
	su := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/signup", body: `{"email":"a@example.com","password":"violet-tractor-umbrella"}`})
	me := decode[auth.Me](t, su)
	sw := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/switch-org", cookie: sessionCookie(t, su), csrf: me.CSRF, origin: publicURL,
		body: `{"org":"` + me.CurrentOrg + `"} {"org":"other"}`})
	if sw.Response.StatusCode() != 400 {
		t.Fatalf("switch with trailing data = %d %s", sw.Response.StatusCode(), sw.Response.Body())
	}
}
