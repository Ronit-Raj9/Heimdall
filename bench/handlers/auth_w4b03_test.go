package handlers_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// TestGetMeHonoursOrgHeader: GET /auth/me with X-Heimdall-Org naming
// another org of the user answers that org (wave 4, organization per tab).
func TestGetMeHonoursOrgHeader(t *testing.T) {
	s := newAuthServer(t, auth.SignupOpen)
	signup := func(email, org string) (string, auth.Me) {
		c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/signup", origin: publicURL,
			body: `{"email":"` + email + `","password":"violet-tractor-umbrella","org_name":"` + org + `"}`})
		if c.Response.StatusCode() != 201 {
			t.Fatalf("signup %s = %d %s", email, c.Response.StatusCode(), c.Response.Body())
		}
		return sessionCookie(t, c), decode[auth.Me](t, c)
	}
	cookieA, meA := signup("a@example.com", "Alpha")
	_, meB := signup("b@example.com", "Beta")
	inv, err := s.svc.CreateInvitationFor(context.Background(), meB.CurrentOrg, "a@example.com", auth.RoleMember, meB.User.ID, "user", "")
	if err != nil {
		t.Fatal(err)
	}
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/invitations/" + inv.Token, cookie: cookieA, csrf: meA.CSRF, origin: publicURL}); c.Response.StatusCode() != 200 {
		t.Fatalf("accept = %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	// Accepting moved the session to Beta; make Alpha the default again.
	if c := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/switch-org", cookie: cookieA, csrf: meA.CSRF, origin: publicURL,
		body: `{"org":"` + meA.CurrentOrg + `"}`}); c.Response.StatusCode() != 200 {
		t.Fatalf("switch = %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	c := s.doWithOrg(t, req{method: "GET", path: handlers.AuthPrefix + "/me", cookie: cookieA}, meB.CurrentOrg)
	got := decode[auth.Me](t, c)
	if got.CurrentOrg != meB.CurrentOrg || got.Role != auth.RoleMember || !strings.Contains(string(c.Response.Body()), `"default_org":"`+meA.CurrentOrg+`"`) {
		t.Fatalf("me with the header = %s", c.Response.Body())
	}
}

// doWithOrg is do with the X-Heimdall-Org header set.
func (s *authServer) doWithOrg(t *testing.T, q req, org string) *fasthttp.RequestCtx {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(q.method)
	ctx.Request.SetRequestURI(q.path)
	ctx.Request.SetHost("heimdall.example")
	ctx.SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP("198.51.100.10"), Port: 4000})
	ctx.Request.Header.SetCookie(auth.SessionCookie, q.cookie)
	ctx.Request.Header.Set(tenancy.OrgHeader, org)
	s.h(ctx)
	return ctx
}
