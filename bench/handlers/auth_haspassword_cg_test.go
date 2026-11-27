package handlers_test

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
)

// TestMeHasPassword: GET /auth/me says has_password for a signed-in user
// with a password (fix wave M8-49), and leaves it out for a visitor and a
// token. Nothing about the password itself is sent.
func TestMeHasPassword(t *testing.T) {
	s := newAuthServer(t, auth.SignupInvite)
	anon := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me"})
	if strings.Contains(string(anon.Response.Body()), "has_password") {
		t.Fatalf("anonymous me = %s", anon.Response.Body())
	}
	sc := s.do(t, req{method: "POST", path: handlers.AuthPrefix + "/signup", origin: publicURL,
		body: `{"email":"owner@example.com","password":"violet-tractor-umbrella","name":"Owner","org_name":"Acme"}`})
	if sc.Response.StatusCode() != 201 {
		t.Fatalf("signup = %d %s", sc.Response.StatusCode(), sc.Response.Body())
	}
	mc := s.do(t, req{method: "GET", path: handlers.AuthPrefix + "/me", cookie: sessionCookie(t, sc)})
	got := decode[handlers.MeResponse](t, mc)
	if mc.Response.StatusCode() != 200 || got.HasPassword == nil || !*got.HasPassword || got.User == nil || got.User.Email != "owner@example.com" {
		t.Fatalf("me = %s", mc.Response.Body())
	}
	if strings.Contains(string(mc.Response.Body()), "argon2") || strings.Contains(string(mc.Response.Body()), "password_hash") {
		t.Fatalf("password data leaked: %s", mc.Response.Body())
	}
}
