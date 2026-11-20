package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

// Regression tests for the hosted auth audit (lane 4-A).

// 4-A-03: a password reset committed between login's hash check and its
// session issuance must not leave the stale login with a fresh session.
func TestLoginRefusesCredentialChangedAfterCheck(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	e.signup(t, "ada@example.com")
	t.Cleanup(func() { afterCredentialCheck = nil })
	afterCredentialCheck = func(string) {
		afterCredentialCheck = nil
		if _, err := SetPassword(bg, e.d, NewHasher(fastParams), "ada@example.com", "orange-bicycle-lantern"); err != nil {
			t.Errorf("reset: %v", err)
		}
	}
	_, sess, err := e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: goodPassword}, "10.0.0.1", "", "")
	if !errors.Is(err, ErrInvalidCredentials) || sess != nil {
		t.Fatalf("stale login = %v, %v; want ErrInvalidCredentials and no session", sess, err)
	}
	var n int
	if err := e.d.QueryRow(bg, `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("sessions after reset = %d, %v; want 0", n, err)
	}
}

// 4-A-03: a password change verified against an older hash must not
// overwrite a newer password set in between.
func TestChangePasswordRefusesConcurrentReset(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, s1 := e.signup(t, "ada@example.com")
	p := e.principal(t, s1)
	t.Cleanup(func() { afterCredentialCheck = nil })
	afterCredentialCheck = func(string) {
		afterCredentialCheck = nil
		if _, err := SetPassword(bg, e.d, NewHasher(fastParams), "ada@example.com", "orange-bicycle-lantern"); err != nil {
			t.Errorf("reset: %v", err)
		}
	}
	err := e.svc.ChangePassword(bg, p, PasswordChangeRequest{Current: goodPassword, New: "another-fine-passphrase"}, "")
	if e, ok := AsError(err); !ok || e.Status != 409 {
		t.Fatalf("change over a newer reset = %v; want 409", err)
	}
	if _, _, err := e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: "orange-bicycle-lantern"}, "10.0.0.2", "", ""); err != nil {
		t.Fatalf("the reset password was overwritten: %v", err)
	}
}

// 4-A-04: concurrent first signups under invite-only admit exactly one.
func TestConcurrentBootstrapAdmitsOne(t *testing.T) {
	e := newTestEnv(t, SignupInvite)
	const n = 6
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			email := string(rune('a'+i)) + "-owner@example.com"
			_, _, err := e.svc.Signup(bg, SignupRequest{Email: email, Password: goodPassword}, "10.0.1."+string(rune('1'+i)), "", "")
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			} else if !errors.Is(err, ErrSignupNotAllowed) {
				t.Errorf("signup %s: %v", email, err)
			}
		}(i)
	}
	wg.Wait()
	if users, _ := e.svc.st.countUsers(bg, e.d.SQL()); ok != 1 || users != 1 {
		t.Fatalf("bootstrap admitted %d signups (%d users); want 1", ok, users)
	}
}

// 4-A-10: concurrent re-invites of one address leave one pending link.
func TestConcurrentReinviteLeavesOnePending(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sess := e.signup(t, "owner@example.com")
	owner := e.principal(t, sess)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.CreateInvitation(bg, owner, owner.OrgID, "new@example.com", RoleMember, ""); err != nil {
				t.Errorf("invite: %v", err)
			}
		}()
	}
	wg.Wait()
	var n int
	if err := e.d.QueryRow(bg, `SELECT COUNT(*) FROM invitations WHERE org_id = ? AND email = ? AND accepted_at IS NULL`,
		owner.OrgID, "new@example.com").Scan(&n); err != nil || n != 1 {
		t.Fatalf("pending invitations = %d, %v; want 1", n, err)
	}
	if _, err := e.svc.CreateInvitationFor(bg, "org_missing", "x@example.com", RoleMember, "u", "user", ""); err == nil {
		t.Fatal("invitation to a missing org accepted")
	}
}

// 4-A-15: a second signup whose organization name is taken still succeeds
// (on Postgres a failed INSERT would poison the transaction).
func TestSignupWithTakenOrgName(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	slugs := map[string]bool{}
	for _, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		u, _, err := e.svc.Signup(bg, SignupRequest{Email: email, Password: goodPassword, OrgName: "Acme"}, "10.0.2.1", "", "")
		if err != nil {
			t.Fatalf("signup %s: %v", email, err)
		}
		orgs, _ := e.svc.st.memberships(bg, e.d.SQL(), u.ID)
		if len(orgs) != 1 || slugs[orgs[0].Slug] {
			t.Fatalf("orgs of %s = %+v", email, orgs)
		}
		slugs[orgs[0].Slug] = true
	}
	if !slugs["acme"] {
		t.Fatalf("base slug not used: %v", slugs)
	}
}

// 4-A-08: a verifier backend failure is 503, not a permanent 401.
func TestRunnerVerifierOutageIsUnavailable(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	mw := Middleware(e.svc.cfg, e.svc)
	t.Cleanup(func() { RegisterRunnerVerifier(nil) })
	RegisterRunnerVerifier(func(_ context.Context, token string) (*Principal, error) {
		if token == "hmr_down" {
			return nil, errors.New("verify runner token: connection refused")
		}
		return nil, ErrInvalidToken
	})
	_, ctx := run(mw, func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer hmr_down") })
	if ctx.Response.StatusCode() != 503 {
		t.Fatalf("outage: status %d; want 503", ctx.Response.StatusCode())
	}
	_, ctx = run(mw, func(r *fasthttp.Request) { r.Header.Set("Authorization", "Bearer hmr_bad") })
	if ctx.Response.StatusCode() != 401 {
		t.Fatalf("bad token: status %d; want 401", ctx.Response.StatusCode())
	}
}

// 4-A-09: a day count that overflows the Duration is refused.
func TestTokenExpiryOverflowRefused(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sess := e.signup(t, "ada@example.com")
	owner := e.principal(t, sess)
	for _, days := range []int{(1 << 48) + 30, 3651} {
		if _, err := e.svc.CreateToken(bg, owner, TokenCreateRequest{Name: "x", ExpiresInDays: days}, ""); err == nil {
			t.Fatalf("expires_in_days=%d accepted", days)
		}
	}
	if _, err := e.svc.CreateToken(bg, owner, TokenCreateRequest{Name: "x", ExpiresInDays: 3650}, ""); err != nil {
		t.Fatalf("3650 days refused: %v", err)
	}
}

// 4-A-11: activity inside the touch window still counts toward idle.
func TestIdleExpiryCountsUntouchedActivity(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sess := e.signup(t, "ada@example.com")
	idle := e.svc.cfg.SessionIdle
	e.clk.Advance(30 * time.Second)
	e.principal(t, sess) // inside the touch window: last_seen not written
	e.clk.Advance(idle - 30*time.Second)
	e.principal(t, sess) // only idle-30s after the last request: alive (touches)
	e.clk.Advance(idle + sessionTouchEvery)
	if p, _ := e.svc.sessionPrincipal(bg, sess.Cookie); p != nil {
		t.Fatal("session outlived idle plus the touch window")
	}
}

// 4-A-12: refusals before the main path still leave an audit row.
func TestEarlyRefusalsAreAudited(t *testing.T) {
	e := newTestEnv(t, SignupClosed)
	if _, _, err := e.svc.Signup(bg, SignupRequest{Email: "a@example.com", Password: goodPassword}, "10.0.3.1", "", ""); !errors.Is(err, ErrSignupNotAllowed) {
		t.Fatalf("closed signup = %v", err)
	}
	if _, _, err := e.svc.Signup(bg, SignupRequest{Email: "not-an-email", Password: goodPassword}, "10.0.3.1", "", ""); err == nil {
		t.Fatal("bad email accepted")
	}
	if got := e.auditCount(t, "auth.signup_failed"); got != 2 {
		t.Fatalf("signup_failed rows = %d; want 2", got)
	}
	for i := 0; i < 40; i++ {
		e.svc.Login(bg, LoginRequest{Email: "x@example.com", Password: "wrong-wrong-wrong"}, "10.0.3.9", "", "")
	}
	if e.auditCount(t, "auth.login_rate_limited") == 0 {
		t.Fatal("IP-limited login left no audit row")
	}
}

// 4-A-17: "Bearer" with no token is a 401, never a cookie fallback.
func TestEmptyBearerDoesNotFallBackToCookie(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sess := e.signup(t, "ada@example.com")
	mw := Middleware(e.svc.cfg, e.svc)
	for _, h := range []string{"Bearer", "Bearer ", "Bearer    ", "bearer \t"} {
		p, ctx := run(mw, func(r *fasthttp.Request) {
			r.Header.Set("Authorization", h)
			r.Header.SetCookie(SessionCookie, sess.Cookie)
		})
		if ctx.Response.StatusCode() != 401 || p != nil {
			t.Fatalf("%q: status %d principal %+v; want 401", h, ctx.Response.StatusCode(), p)
		}
	}
	// Another scheme still falls through to the cookie.
	p, ctx := run(mw, func(r *fasthttp.Request) {
		r.Header.Set("Authorization", "Basic Zm9vOmJhcg==")
		r.Header.SetCookie(SessionCookie, sess.Cookie)
	})
	if ctx.Response.StatusCode() != 200 || p == nil || p.Kind != KindUser {
		t.Fatalf("basic + cookie: status %d principal %+v", ctx.Response.StatusCode(), p)
	}
}

// 4-A-02: logged paths never carry invitation tokens.
func TestLogPathMasksInvitationTokens(t *testing.T) {
	for in, want := range map[string]string{
		"/invite/abc123":                         "/invite/{token}",
		"/api/bench/auth/invitations/abc123":     "/api/bench/auth/invitations/{token}",
		"/api/bench/auth/invitations/abc/accept": "/api/bench/auth/invitations/{token}/accept",
		"/api/bench/orgs/acme/invitations":       "/api/bench/orgs/acme/invitations",
		"/api/bench/experiments":                 "/api/bench/experiments",
	} {
		if got := LogPath([]byte(in)); got != want {
			t.Errorf("LogPath(%q) = %q; want %q", in, got, want)
		}
	}
}

// 4-A-19: linking GitHub by email retires the unverified password and the
// sessions opened with it.
func TestGitHubLinkRetiresPriorCredentials(t *testing.T) {
	f := newFakeGitHub(t)
	e := newTestEnv(t, SignupOpen)
	withGitHub(e, f)
	_, pwSess := e.signup(t, "ada@example.com")
	f.add("ok", fakeGHUser{ID: 7, Login: "ada", Emails: primary("ada@example.com", true)})
	ghSess, _, err := ghFlow(t, e, "ok", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.sessionPrincipal(bg, pwSess.Cookie); p != nil {
		t.Fatal("password session survived GitHub linking")
	}
	if _, _, err := e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: goodPassword}, "10.0.4.1", "", ""); err == nil {
		t.Fatal("unverified password still signs in to the linked account")
	}
	if e.auditCount(t, "auth.github_link") != 1 {
		t.Fatal("link not audited")
	}
	// The GitHub owner can set a password with no current one.
	if err := e.svc.ChangePassword(bg, e.principal(t, ghSess), PasswordChangeRequest{New: "another-fine-passphrase"}, ""); err != nil {
		t.Fatal(err)
	}
}
