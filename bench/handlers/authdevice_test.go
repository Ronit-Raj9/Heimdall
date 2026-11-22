package handlers_test

import (
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func newDeviceServer(t *testing.T) *authServer {
	t.Helper()
	s := newAuthServer(t, auth.SignupInvite)
	r := router.New()
	mw := lib.BodyLimit(schemas.ModeLive, lib.MaxBodyBytes)
	handlers.NewAuthHandler(schemas.ModeLive, s.svc).RegisterRoutes(r, mw)
	handlers.NewDeviceHandler(schemas.ModeLive, s.svc).RegisterRoutes(r, mw)
	cfg := s.svc.Config()
	s.h = lib.ChainMiddlewares(r.Handler, auth.Middleware(cfg, s.svc))
	return s
}

func TestDeviceLoginRoutes(t *testing.T) {
	s := newDeviceServer(t)
	sc := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/signup", origin: publicURL,
		body: `{"email":"owner@example.com","password":"violet-tractor-umbrella","name":"Owner","org_name":"Acme"}`})
	cookie := sessionCookie(t, sc)
	me := decode[auth.Me](t, sc)

	// The CLI asks for a code (no origin, no cookie: a non-browser client).
	cc := s.do(t, req{method: "POST", path: handlers.DeviceCodePath, body: `{"client_id":"heimdall-cli","client_name":"heimdall CLI on box"}`})
	if cc.Response.StatusCode() != 200 {
		t.Fatalf("code = %d %s", cc.Response.StatusCode(), cc.Response.Body())
	}
	code := decode[auth.DeviceCodeResponse](t, cc)
	if code.VerificationURI != publicURL+"/device" || code.DeviceCode == "" {
		t.Fatalf("code = %+v", code)
	}
	poll := func() (int, map[string]any) {
		c := s.do(t, req{method: "POST", path: handlers.DeviceTokenPath,
			body: `{"grant_type":"` + auth.DeviceGrantType + `","device_code":"` + code.DeviceCode + `"}`})
		var m map[string]any
		_ = json.Unmarshal(c.Response.Body(), &m)
		return c.Response.StatusCode(), m
	}
	if st, m := poll(); st != 400 || m["error"] != auth.DeviceErrPending {
		t.Fatalf("pending poll = %d %v", st, m)
	}

	// Lookup and approve need the browser session (and CSRF on the write).
	lookup := handlers.DeviceLookupPath + "?user_code=" + url.QueryEscape(strings.ToLower(code.UserCode))
	if c := s.do(t, req{method: "GET", path: lookup}); c.Response.StatusCode() != 403 {
		t.Fatalf("anonymous lookup = %d", c.Response.StatusCode())
	}
	lc := s.do(t, req{method: "GET", path: lookup, cookie: cookie})
	if info := decode[auth.DeviceInfo](t, lc); lc.Response.StatusCode() != 200 || info.ClientName != "heimdall CLI on box" {
		t.Fatalf("lookup = %d %s", lc.Response.StatusCode(), lc.Response.Body())
	}
	body := `{"user_code":"` + code.UserCode + `"}`
	if c := s.do(t, req{method: "POST", path: handlers.DeviceApprovePath, cookie: cookie, origin: publicURL, body: body}); c.Response.StatusCode() != 403 {
		t.Fatalf("approve without CSRF = %d", c.Response.StatusCode())
	}
	ac := s.do(t, req{method: "POST", path: handlers.DeviceApprovePath, cookie: cookie, csrf: me.CSRF, origin: publicURL, body: body})
	if ac.Response.StatusCode() != 200 {
		t.Fatalf("approve = %d %s", ac.Response.StatusCode(), ac.Response.Body())
	}

	// A form-encoded poll (the RFC wire format) gets the token once.
	form := url.Values{"grant_type": {auth.DeviceGrantType}, "device_code": {code.DeviceCode}}.Encode()
	tc := s.doForm(t, handlers.DeviceTokenPath, form)
	if tc.Response.StatusCode() != 200 {
		t.Fatalf("token = %d %s", tc.Response.StatusCode(), tc.Response.Body())
	}
	tok := decode[auth.DeviceTokenResponse](t, tc)
	if !strings.HasPrefix(tok.AccessToken, auth.APITokenPrefix) || tok.Scope != "evaluate" {
		t.Fatalf("token = %+v", tok)
	}
	// The token works as a bearer token.
	if c := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me", bearer: tok.AccessToken}); c.Response.StatusCode() != 200 {
		t.Fatalf("me with device token = %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	if st, m := poll(); st != 400 || m["error"] != auth.DeviceErrInvalid {
		t.Fatalf("poll after hand-over = %d %v", st, m)
	}
}

func (s *authServer) doForm(t *testing.T, path, form string) *fasthttp.RequestCtx {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(path)
	ctx.Request.SetHost("heimdall.example")
	ctx.SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP("198.51.100.10"), Port: 4000})
	ctx.Request.Header.SetContentType("application/x-www-form-urlencoded")
	ctx.Request.SetBodyString(form)
	s.h(ctx)
	return ctx
}

func TestDeviceLoginLocalMode(t *testing.T) {
	r := router.New()
	handlers.NewDeviceHandler(schemas.ModeLive, nil).RegisterRoutes(r)
	s := &authServer{h: r.Handler}
	if c := s.do(t, req{method: "POST", path: handlers.DeviceCodePath}); c.Response.StatusCode() != 404 ||
		!strings.Contains(string(c.Response.Body()), "local server needs no login") {
		t.Fatalf("local = %d %s", c.Response.StatusCode(), c.Response.Body())
	}
}
