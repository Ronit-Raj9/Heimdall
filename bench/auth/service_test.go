package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type testEnv struct {
	svc *Service
	d   *db.DB
	clk *fakeClock
}

func newTestEnv(t *testing.T, signup SignupPolicy) *testEnv {
	t.Helper()
	d := dbtest.Open(t)
	clk := newFakeClock()
	cfg := Config{Hosted: true, ServerMode: schemas.ModeLive, PublicURL: "https://heimdall.example", Signup: signup,
		SessionTTL: 48 * time.Hour, SessionIdle: 6 * time.Hour, InviteTTL: 72 * time.Hour,
		AllowedOrigins: []string{"https://heimdall.example"}}
	svc, err := New(cfg, Deps{DB: d, Hasher: NewHasher(fastParams), Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{svc: svc, d: d, clk: clk}
}

var bg = context.Background()

const goodPassword = "violet-tractor-umbrella"

// signup creates an account and verifies its email, the state most tests
// need; signupUnverified leaves it unverified (verify_test.go).
func (e *testEnv) signup(t *testing.T, email string) (*User, *NewSession) {
	t.Helper()
	u, s := e.signupUnverified(t, email)
	if _, err := MarkEmailVerified(bg, e.d, email); err != nil {
		t.Fatalf("verify %s: %v", email, err)
	}
	return u, s
}

func (e *testEnv) signupUnverified(t *testing.T, email string) (*User, *NewSession) {
	t.Helper()
	u, s, err := e.svc.Signup(bg, SignupRequest{Email: email, Password: goodPassword, Name: strings.Split(email, "@")[0]}, "10.0.0.1", "test", "")
	if err != nil {
		t.Fatalf("signup %s: %v", email, err)
	}
	return u, s
}

func (e *testEnv) principal(t *testing.T, sess *NewSession) *Principal {
	t.Helper()
	p, err := e.svc.sessionPrincipal(bg, sess.Cookie)
	if err != nil || p == nil {
		t.Fatalf("session principal = %v, %v", p, err)
	}
	return p
}

func (e *testEnv) auditCount(t *testing.T, action string) int {
	t.Helper()
	var n int
	if err := e.d.QueryRow(bg, `SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBootstrapSignupThenPolicy(t *testing.T) {
	e := newTestEnv(t, SignupInvite)
	if !e.svc.Info(bg).Bootstrap {
		t.Fatal("empty install not reported as bootstrap")
	}
	u, sess := e.signup(t, "Owner@Example.com")
	if u.Email != "owner@example.com" {
		t.Fatalf("email not normalised: %s", u.Email)
	}
	p := e.principal(t, sess)
	if p.Kind != KindUser || p.Role != RoleOwner || p.OrgID == "" || p.OrgID != sess.OrgID {
		t.Fatalf("bootstrap principal = %+v", p)
	}
	if e.svc.Info(bg).Bootstrap {
		t.Fatal("still bootstrap after the first account")
	}
	_, _, err := e.svc.Signup(bg, SignupRequest{Email: "second@example.com", Password: goodPassword}, "10.0.0.2", "", "")
	if !errors.Is(err, ErrSignupNotAllowed) {
		t.Fatalf("second signup under invite = %v, want ErrSignupNotAllowed", err)
	}
	if e.auditCount(t, "auth.signup") != 1 || e.auditCount(t, "auth.signup_failed") != 1 {
		t.Fatal("signup audit rows missing")
	}
}

func TestOpenSignupAndDuplicateEmail(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, s1 := e.signup(t, "a@example.com")
	_, s2 := e.signup(t, "b@example.com")
	if s1.OrgID == s2.OrgID {
		t.Fatal("open signup reused an org")
	}
	_, _, err := e.svc.Signup(bg, SignupRequest{Email: "A@example.com ", Password: goodPassword}, "10.0.0.3", "", "")
	if !errors.Is(err, ErrSignupFailed) {
		t.Fatalf("duplicate signup = %v, want ErrSignupFailed", err)
	}
	_, _, err = e.svc.Signup(bg, SignupRequest{Email: "c@example.com", Password: "password1234"}, "10.0.0.3", "", "")
	if e, ok := AsError(err); !ok || e.Status != 400 {
		t.Fatalf("weak password signup = %v, want 400", err)
	}
	_, _, err = e.svc.Signup(bg, SignupRequest{Email: "not-an-email", Password: goodPassword}, "10.0.0.4", "", "")
	if e, ok := AsError(err); !ok || e.Status != 400 {
		t.Fatalf("bad email signup = %v, want 400", err)
	}
}

func TestClosedSignupRefusesEvenBootstrap(t *testing.T) {
	e := newTestEnv(t, SignupClosed)
	if e.svc.Info(bg).Bootstrap {
		t.Fatal("closed install offers bootstrap")
	}
	if _, _, err := e.svc.Signup(bg, SignupRequest{Email: "a@example.com", Password: goodPassword}, "10.0.0.1", "", ""); !errors.Is(err, ErrSignupNotAllowed) {
		t.Fatalf("closed signup = %v", err)
	}
}

func TestLoginUniformFailures(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	u, _ := e.signup(t, "ada@example.com")
	disabled, _ := e.signup(t, "off@example.com")
	if _, err := e.d.Exec(bg, `UPDATE users SET disabled = 1 WHERE id = ?`, disabled.ID); err != nil {
		t.Fatal(err)
	}
	cases := []LoginRequest{
		{Email: "ada@example.com", Password: "wrong-password-123"},
		{Email: "nobody@example.com", Password: goodPassword},
		{Email: "off@example.com", Password: goodPassword},
		{Email: "", Password: ""},
		{Email: "ada@example.com", Password: strings.Repeat("x", 2000)},
	}
	for i, req := range cases {
		_, _, err := e.svc.Login(bg, req, "10.1.0."+string(rune('1'+i)), "", "")
		if err != ErrInvalidCredentials {
			t.Errorf("case %d: err = %v, want exactly ErrInvalidCredentials", i, err)
		}
	}
	got, sess, err := e.svc.Login(bg, LoginRequest{Email: " ADA@example.com", Password: goodPassword}, "10.1.0.9", "ua", "")
	if err != nil || got.ID != u.ID || sess.Cookie == "" || sess.CSRF == "" {
		t.Fatalf("good login = %v, %v, %v", got, sess, err)
	}
	if e.auditCount(t, "auth.login_failed") != len(cases) || e.auditCount(t, "auth.login") != 1 {
		t.Fatalf("login audit: failed %d ok %d", e.auditCount(t, "auth.login_failed"), e.auditCount(t, "auth.login"))
	}
}

func TestLoginRateLimits(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	e.signup(t, "ada@example.com")
	var err error
	n := 0
	for ; n < 50; n++ {
		// A fresh IP each time: only the per-email bucket can stop this.
		_, _, err = e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: "wrong-password-123"}, "10.2."+string(rune('a'+n%26))+".1", "", "")
		if err != ErrInvalidCredentials {
			break
		}
	}
	ae, ok := AsError(err)
	if !ok || ae.Status != 429 || ae.RetryAfter <= 0 || n != LoginPerEmail.Burst {
		t.Fatalf("after %d failures err = %v, want 429 after %d", n, err, LoginPerEmail.Burst)
	}
	// The rate limit answers the same for an unknown email: no enumeration.
	for i := 0; i < LoginPerEmail.Burst; i++ {
		e.svc.Login(bg, LoginRequest{Email: "ghost@example.com", Password: "x"}, "10.3.0.1", "", "")
	}
	_, _, err2 := e.svc.Login(bg, LoginRequest{Email: "ghost@example.com", Password: "x"}, "10.3.0.2", "", "")
	if ae2, ok := AsError(err2); !ok || ae2.Status != 429 || ae2.Msg != ae.Msg {
		t.Fatalf("unknown email limit = %v", err2)
	}
	// After the refill a correct password works again.
	e.clk.Advance(LoginPerEmail.Every * time.Duration(LoginPerEmail.Burst))
	if _, _, err := e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: goodPassword}, "10.2.0.1", "", ""); err != nil {
		t.Fatalf("login after refill = %v", err)
	}
	// Per IP: one address spraying many emails.
	e2 := newTestEnv(t, SignupOpen)
	for n = 0; n < 100; n++ {
		_, _, err = e2.svc.Login(bg, LoginRequest{Email: "u" + string(rune('a'+n%26)) + string(rune('a'+n/26)) + "@example.com", Password: "x"}, "10.9.9.9", "", "")
		if err != ErrInvalidCredentials {
			break
		}
	}
	if ae, ok := AsError(err); !ok || ae.Status != 429 || n != LoginPerIP.Burst {
		t.Fatalf("per-IP limit after %d: %v", n, err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sess := e.signup(t, "ada@example.com")
	p := e.principal(t, sess)
	if p.SessionID() != HashSecret(sess.Cookie) || p.csrf != sess.CSRF {
		t.Fatal("principal does not carry the session")
	}
	// The stored id is the hash, never the cookie value.
	var n int
	e.d.QueryRow(bg, `SELECT COUNT(*) FROM sessions WHERE id = ?`, sess.Cookie).Scan(&n)
	if n != 0 {
		t.Fatal("session stored unhashed")
	}
	// Idle expiry: activity keeps it alive, silence ends it.
	e.clk.Advance(5 * time.Hour)
	e.principal(t, sess) // touches last_seen
	e.clk.Advance(5 * time.Hour)
	e.principal(t, sess)
	e.clk.Advance(7 * time.Hour)
	if p, _ := e.svc.sessionPrincipal(bg, sess.Cookie); p != nil {
		t.Fatal("idle session still valid")
	}
	// Absolute expiry even with activity.
	_, s2, _ := e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: goodPassword}, "10.0.0.1", "", "")
	for i := 0; i < 10; i++ {
		e.clk.Advance(5 * time.Hour)
		p, _ := e.svc.sessionPrincipal(bg, s2.Cookie)
		if i < 9 && p == nil {
			t.Fatalf("session ended early at %dh", (i+1)*5)
		}
		if i == 9 && p != nil {
			t.Fatal("session outlived session_ttl")
		}
	}
}

func TestLoginRotatesAndLogoutDeletes(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, s1 := e.signup(t, "ada@example.com")
	_, s2, err := e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: goodPassword}, "10.0.0.1", "", HashSecret(s1.Cookie))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.sessionPrincipal(bg, s1.Cookie); p != nil {
		t.Fatal("old session survived login")
	}
	p := e.principal(t, s2)
	if err := e.svc.Logout(bg, p, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.sessionPrincipal(bg, s2.Cookie); p != nil {
		t.Fatal("session survived logout")
	}
	if e.auditCount(t, "auth.logout") != 1 {
		t.Fatal("logout not audited")
	}
}

func TestDisabledUserLosesSession(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	u, sess := e.signup(t, "ada@example.com")
	e.d.Exec(bg, `UPDATE users SET disabled = 1 WHERE id = ?`, u.ID)
	if p, _ := e.svc.sessionPrincipal(bg, sess.Cookie); p != nil {
		t.Fatal("disabled user's session still valid")
	}
}

func TestChangePassword(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, s1 := e.signup(t, "ada@example.com")
	_, s2, _ := e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: goodPassword}, "10.0.0.1", "", "")
	p := e.principal(t, s1)
	if err := e.svc.ChangePassword(bg, p, PasswordChangeRequest{Current: "nope-nope-nope", New: "another-fine-passphrase"}, ""); err == nil {
		t.Fatal("wrong current password accepted")
	}
	if err := e.svc.ChangePassword(bg, p, PasswordChangeRequest{Current: goodPassword, New: "short"}, ""); err == nil {
		t.Fatal("weak new password accepted")
	}
	if err := e.svc.ChangePassword(bg, p, PasswordChangeRequest{Current: goodPassword, New: "another-fine-passphrase"}, ""); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.sessionPrincipal(bg, s2.Cookie); p != nil {
		t.Fatal("other session survived a password change")
	}
	e.principal(t, s1) // this one stays
	if _, _, err := e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: "another-fine-passphrase"}, "10.0.0.2", "", ""); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	tok := &Principal{Kind: KindToken, UserID: p.UserID}
	if err := e.svc.ChangePassword(bg, tok, PasswordChangeRequest{}, ""); !errors.Is(err, ErrSessionRequired) {
		t.Fatalf("token changed a password: %v", err)
	}
}

func TestLoginRehashesOnParamChange(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	u, _ := e.signup(t, "ada@example.com")
	stronger := fastParams
	stronger.Time = 2
	e.svc.hasher = NewHasher(stronger)
	if _, _, err := e.svc.Login(bg, LoginRequest{Email: "ada@example.com", Password: goodPassword}, "10.0.0.1", "", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.st.userByID(bg, e.d.SQL(), u.ID)
	if !strings.Contains(got.PasswordHash, ",t=2,") {
		t.Fatalf("hash not upgraded: %s", got.PasswordHash)
	}
}

func TestAPITokens(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, ownerSess := e.signup(t, "owner@example.com")
	owner := e.principal(t, ownerSess)
	member, memberSess := e.signup(t, "member@example.com")
	if err := e.svc.st.grantMembership(bg, e.d.SQL(), owner.OrgID, member.ID, RoleMember); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SwitchOrg(bg, e.principal(t, memberSess), owner.OrgID, ""); err != nil {
		t.Fatal(err)
	}
	mp := e.principal(t, memberSess)
	if mp.Role != RoleMember || mp.OrgID != owner.OrgID {
		t.Fatalf("member principal = %+v", mp)
	}
	if _, err := e.svc.CreateToken(bg, mp, TokenCreateRequest{Name: "ci"}, ""); err == nil {
		t.Fatal("member created a token")
	}
	if err := e.svc.st.grantMembership(bg, e.d.SQL(), owner.OrgID, member.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	admin := e.principal(t, memberSess)
	if _, err := e.svc.CreateToken(bg, admin, TokenCreateRequest{Name: "ci", Role: RoleOwner}, ""); err == nil {
		t.Fatal("admin created an owner token")
	}
	created, err := e.svc.CreateToken(bg, admin, TokenCreateRequest{Name: "ci", Role: RoleMember, ExpiresInDays: 30}, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, "hm_"+created.APIToken.Prefix+"_") || created.APIToken.ExpiresAt == nil {
		t.Fatalf("created = %+v", created)
	}
	tp, err := e.svc.tokenPrincipal(bg, created.Token)
	if err != nil || tp.Kind != KindToken || tp.Role != RoleMember || tp.OrgID != owner.OrgID || tp.TokenID != created.APIToken.ID {
		t.Fatalf("token principal = %+v, %v", tp, err)
	}
	// A token can not mint tokens.
	if _, err := e.svc.CreateToken(bg, tp, TokenCreateRequest{Name: "x"}, ""); !errors.Is(err, ErrSessionRequired) {
		t.Fatalf("token minted a token: %v", err)
	}
	// Demoting the creator narrows the token.
	e.d.Exec(bg, `UPDATE memberships SET role = 'viewer' WHERE user_id = ?`, member.ID)
	tp, _ = e.svc.tokenPrincipal(bg, created.Token)
	if tp.Role != RoleViewer {
		t.Fatalf("token role after demotion = %s", tp.Role)
	}
	// Removing the creator kills it.
	e.d.Exec(bg, `DELETE FROM memberships WHERE user_id = ? AND org_id = ?`, member.ID, owner.OrgID)
	if _, err := e.svc.tokenPrincipal(bg, created.Token); err != ErrInvalidToken {
		t.Fatalf("removed creator's token = %v", err)
	}
	// Owner token: list, revoke, expiry, unknown and tampered tokens.
	oc, err := e.svc.CreateToken(bg, owner, TokenCreateRequest{Name: "deploy", ExpiresInDays: 1}, "")
	if err != nil || oc.APIToken.Role != RoleOwner {
		t.Fatalf("owner token = %+v, %v", oc, err)
	}
	list, err := e.svc.ListTokens(bg, owner)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	// Tampering with any character of the secret or the lookup prefix is
	// refused. (Each position is changed to a different character, so the
	// "tampered" token never equals the real one.)
	for _, i := range []int{3, 10, 12, 20, len(oc.Token) - 1} {
		if bad := tamper(oc.Token, i); bad == oc.Token {
			t.Fatalf("tamper(%d) left the token unchanged", i)
		} else if _, err := e.svc.tokenPrincipal(bg, bad); err != ErrInvalidToken {
			t.Fatalf("token tampered at %d = %v", i, err)
		}
	}
	if _, err := e.svc.tokenPrincipal(bg, oc.Token); err != nil {
		t.Fatalf("real token after tamper checks = %v", err)
	}
	if _, err := e.svc.tokenPrincipal(bg, "hm_nonsense"); err != ErrInvalidToken {
		t.Fatalf("malformed token = %v", err)
	}
	e.clk.Advance(25 * time.Hour)
	if _, err := e.svc.tokenPrincipal(bg, oc.Token); err != ErrInvalidToken {
		t.Fatalf("expired token = %v", err)
	}
	oc2, _ := e.svc.CreateToken(bg, owner, TokenCreateRequest{Name: "forever"}, "")
	if _, err := e.svc.tokenPrincipal(bg, oc2.Token); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeToken(bg, owner, oc2.APIToken.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.tokenPrincipal(bg, oc2.Token); err != ErrInvalidToken {
		t.Fatalf("revoked token = %v", err)
	}
	if err := e.svc.RevokeToken(bg, owner, oc2.APIToken.ID, ""); err == nil {
		t.Fatal("revoked twice")
	}
	// Another org's admin can not revoke or see this org's tokens.
	_, otherSess := e.signup(t, "other@example.com")
	other := e.principal(t, otherSess)
	if err := e.svc.RevokeToken(bg, other, created.APIToken.ID, ""); err == nil {
		t.Fatal("other org revoked a token")
	}
	ol, _ := e.svc.ListTokens(bg, other)
	if len(ol) != 0 {
		t.Fatal("other org sees tokens")
	}
	if e.auditCount(t, "auth.token_create") != 3 || e.auditCount(t, "auth.token_revoke") != 1 {
		t.Fatal("token audit rows missing")
	}
	// The secret is stored only as its hash.
	var stored string
	e.d.QueryRow(bg, `SELECT hash FROM api_tokens WHERE id = ?`, oc2.APIToken.ID).Scan(&stored)
	if stored != HashSecret(oc2.Token) || strings.Contains(stored, oc2.Token) {
		t.Fatal("token not stored as sha256")
	}
}

func TestTokenLastUsedThrottled(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sess := e.signup(t, "owner@example.com")
	c, _ := e.svc.CreateToken(bg, e.principal(t, sess), TokenCreateRequest{Name: "t"}, "")
	used := func() int64 {
		var v *int64
		e.d.QueryRow(bg, `SELECT last_used_at FROM api_tokens WHERE id = ?`, c.APIToken.ID).Scan(&v)
		if v == nil {
			return 0
		}
		return *v
	}
	e.svc.tokenPrincipal(bg, c.Token)
	first := used()
	if first == 0 {
		t.Fatal("last_used_at not set")
	}
	e.clk.Advance(time.Minute)
	e.svc.tokenPrincipal(bg, c.Token)
	if used() != first {
		t.Fatal("last_used_at written within the throttle window")
	}
	e.clk.Advance(5 * time.Minute)
	e.svc.tokenPrincipal(bg, c.Token)
	if used() == first {
		t.Fatal("last_used_at not refreshed after the window")
	}
}

func TestInvitations(t *testing.T) {
	e := newTestEnv(t, SignupInvite)
	_, ownerSess := e.signup(t, "owner@example.com") // bootstrap
	owner := e.principal(t, ownerSess)
	if _, err := e.svc.CreateInvitation(bg, &Principal{Kind: KindUser, UserID: "x", OrgID: owner.OrgID, Role: RoleMember}, owner.OrgID, "x@example.com", RoleViewer, ""); err == nil {
		t.Fatal("member invited")
	}
	inv, err := e.svc.CreateInvitation(bg, owner, owner.OrgID, "New@Example.com", RoleMember, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(inv.URL, "https://heimdall.example/invite/") || inv.Invitation.Email != "new@example.com" {
		t.Fatalf("invitation = %+v", inv)
	}
	info, err := e.svc.InvitationInfo(bg, inv.Token, "10.0.0.1")
	if err != nil || info.Email != "new@example.com" || info.Role != RoleMember || info.AccountExists || !info.SignupAllowed {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if _, err := e.svc.InvitationInfo(bg, "bogus", "10.0.0.1"); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("bogus info = %v", err)
	}
	// Signup with the invitation but another email.
	if _, _, err := e.svc.Signup(bg, SignupRequest{Email: "intruder@example.com", Password: goodPassword, Invitation: inv.Token}, "10.0.0.5", "", ""); !errors.Is(err, ErrInvitationEmail) {
		t.Fatalf("mismatched signup = %v", err)
	}
	u, sess, err := e.svc.Signup(bg, SignupRequest{Email: "new@example.com", Password: goodPassword, Invitation: inv.Token}, "10.0.0.5", "", "")
	if err != nil {
		t.Fatal(err)
	}
	p := e.principal(t, sess)
	if p.OrgID != owner.OrgID || p.Role != RoleMember || p.UserID != u.ID {
		t.Fatalf("invited principal = %+v", p)
	}
	if _, _, err := e.svc.Signup(bg, SignupRequest{Email: "new2@example.com", Password: goodPassword, Invitation: inv.Token}, "10.0.0.6", "", ""); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("reused invitation = %v", err)
	}
	// An existing user accepts an invitation to a second org.
	_, otherSess := e.signup2(t, e, "boss@example.com")
	boss := e.principal(t, otherSess)
	inv2, err := e.svc.CreateInvitation(bg, boss, boss.OrgID, "new@example.com", RoleAdmin, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptInvitation(bg, owner, inv2.Token, ""); !errors.Is(err, ErrInvitationEmail) {
		t.Fatalf("wrong user accepted: %v", err)
	}
	o, err := e.svc.AcceptInvitation(bg, p, inv2.Token, "")
	if err != nil || o.ID != boss.OrgID || o.Role != RoleAdmin {
		t.Fatalf("accept = %+v, %v", o, err)
	}
	if p2 := e.principal(t, sess); p2.OrgID != boss.OrgID || p2.Role != RoleAdmin {
		t.Fatalf("session not switched to the new org: %+v", p2)
	}
	// Expired invitations are dead.
	inv3, _ := e.svc.CreateInvitation(bg, owner, owner.OrgID, "late@example.com", RoleViewer, "")
	e.clk.Advance(73 * time.Hour)
	if _, err := e.svc.InvitationInfo(bg, inv3.Token, "10.0.0.1"); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("expired info = %v", err)
	}
	// Revoke and list.
	inv4, _ := e.svc.CreateInvitation(bg, owner, owner.OrgID, "gone@example.com", RoleViewer, "")
	if err := e.svc.RevokeInvitation(bg, owner, owner.OrgID, inv4.Invitation.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.InvitationInfo(bg, inv4.Token, "10.0.0.1"); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatal("revoked invitation still works")
	}
	list, _ := e.svc.ListInvitations(bg, owner.OrgID)
	if len(list) != 2 { // the accepted one and the expired one
		t.Fatalf("invitations listed = %d", len(list))
	}
	if e.auditCount(t, "auth.invite_create") != 4 || e.auditCount(t, "auth.invite_accept") != 2 || e.auditCount(t, "auth.invite_revoke") != 1 {
		t.Fatal("invitation audit rows missing")
	}
}

// signup2 creates a second independent account and org on an invite-only
// install by inserting it directly (the web refuses it, by design).
func (e *testEnv) signup2(t *testing.T, _ *testEnv, email string) (*User, *NewSession) {
	t.Helper()
	hash, _ := e.svc.hasher.Hash(goodPassword)
	u, err := e.svc.st.insertUser(bg, e.d.SQL(), email, "", hash, "")
	if err != nil {
		t.Fatal(err)
	}
	o, err := e.svc.st.insertOrg(bg, e.d.SQL(), "Boss org")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.st.grantMembership(bg, e.d.SQL(), o.ID, u.ID, RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.st.markVerified(bg, e.d.SQL(), u.ID, u.Email, VerifiedByOperator); err != nil {
		t.Fatal(err)
	}
	s, err := e.svc.createSession(bg, e.d.SQL(), u.ID, o.ID, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return u, s
}

func TestSwitchOrgAndMe(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, s1 := e.signup(t, "a@example.com")
	_, s2 := e.signup(t, "b@example.com")
	a := e.principal(t, s1)
	b := e.principal(t, s2)
	if _, err := e.svc.SwitchOrg(bg, a, b.OrgID, ""); err == nil {
		t.Fatal("switched into a foreign org")
	} else if ae, _ := AsError(err); ae.Status != 404 {
		t.Fatalf("foreign org answer = %v, want 404 like an unknown org", err)
	}
	if _, err := e.svc.SwitchOrg(bg, a, "org_doesnotexist", ""); err == nil {
		t.Fatal("switched into nothing")
	}
	me, err := e.svc.Me(bg, a)
	if err != nil || me.User.Email != "a@example.com" || len(me.Orgs) != 1 || me.CSRF != s1.CSRF || me.CurrentOrg != a.OrgID || me.Role != RoleOwner {
		t.Fatalf("me = %+v, %v", me, err)
	}
	slug := me.Orgs[0].Slug
	if o, err := e.svc.SwitchOrg(bg, a, slug, ""); err != nil || o.ID != a.OrgID {
		t.Fatalf("switch by slug = %+v, %v", o, err)
	}
	// A visitor with no credential gets a signed-out answer, not an error
	// (TestMeAnonymousIs200).
	if me, err := e.svc.Me(bg, Anonymous()); err != nil || me.Kind != string(KindAnonymous) {
		t.Fatalf("anonymous me = %+v, %v", me, err)
	}
}

// TestMeAnonymousIs200 covers G1.console: a caller with no credential at
// all is answered with an anonymous Me (no error, so 200), so the landing
// page and docs show no 401 in the browser console.
func TestMeAnonymousIs200(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/bench/auth/me")
	p, err := e.svc.Authenticate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	me, err := e.svc.Me(bg, p)
	if err != nil {
		t.Fatalf("anonymous me = %v, want no error", err)
	}
	if me.AuthMode != "hosted" || me.Kind != "anonymous" || me.Orgs == nil || len(me.Orgs) != 0 || me.CSRF != "" || me.User != nil {
		t.Fatalf("anonymous me = %+v", me)
	}
}

// TestMeExpiredSessionIs401 checks that a presented session cookie that no
// longer names a live session still answers 401: the UI reads 401 as
// "signed out" and must keep doing so.
func TestMeExpiredSessionIs401(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sess := e.signup(t, "a@example.com")
	e.clk.Advance(72 * time.Hour)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("/api/bench/auth/me")
	ctx.Request.Header.SetCookie(SessionCookie, sess.Cookie)
	p, err := e.svc.Authenticate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Me(bg, p); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session me = %v, want ErrUnauthenticated", err)
	}
	ctx2 := &fasthttp.RequestCtx{}
	ctx2.Request.Header.SetMethod("GET")
	ctx2.Request.SetRequestURI("/api/bench/auth/me")
	ctx2.Request.Header.SetCookie(SessionCookie, "not-a-session")
	p2, err := e.svc.Authenticate(ctx2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Me(bg, p2); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unknown session me = %v, want ErrUnauthenticated", err)
	}
}

func TestCLICreateUserAndSetPassword(t *testing.T) {
	e := newTestEnv(t, SignupClosed)
	h := NewHasher(fastParams)
	c, err := CreateUser(bg, e.d, h, "Root@Example.com", "Root", goodPassword, "Acme Corp", RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if !c.OrgCreated || c.Org.Slug != "acme-corp" || c.User.Email != "root@example.com" {
		t.Fatalf("created = %+v", c)
	}
	// A second user joins the existing org by slug.
	c2, err := CreateUser(bg, e.d, h, "two@example.com", "", goodPassword, "acme-corp", RoleViewer)
	if err != nil || c2.OrgCreated || c2.Org.ID != c.Org.ID {
		t.Fatalf("join = %+v, %v", c2, err)
	}
	if _, err := CreateUser(bg, e.d, h, "root@example.com", "", goodPassword, "x", RoleOwner); err == nil {
		t.Fatal("duplicate email created")
	}
	if _, err := CreateUser(bg, e.d, h, "weak@example.com", "", "password1234", "x", RoleOwner); err == nil {
		t.Fatal("weak password accepted")
	}
	// The CLI owner can sign in on a closed install.
	_, sess, err := e.svc.Login(bg, LoginRequest{Email: "root@example.com", Password: goodPassword}, "10.0.0.1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if p := e.principal(t, sess); p.OrgID != c.Org.ID || p.Role != RoleOwner {
		t.Fatalf("CLI owner principal = %+v", p)
	}
	if _, err := SetPassword(bg, e.d, h, "root@example.com", "orange-bicycle-lantern"); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.sessionPrincipal(bg, sess.Cookie); p != nil {
		t.Fatal("session survived set-password")
	}
	if _, _, err := e.svc.Login(bg, LoginRequest{Email: "root@example.com", Password: "orange-bicycle-lantern"}, "10.0.0.2", "", ""); err != nil {
		t.Fatalf("login with the new password: %v", err)
	}
	if _, err := SetPassword(bg, e.d, h, "ghost@example.com", "orange-bicycle-lantern"); err == nil {
		t.Fatal("set-password of a missing user")
	}
	if e.auditCount(t, "auth.user_create") != 2 || e.auditCount(t, "auth.password_set") != 1 {
		t.Fatal("CLI audit rows missing")
	}
}

// tamper changes the character at i to a different one of the same class.
func tamper(tok string, i int) string {
	b := []byte(tok)
	switch {
	case b[i] == 'a':
		b[i] = 'b'
	case b[i] >= 'a' && b[i] <= 'z', b[i] >= '0' && b[i] <= '9', b[i] >= 'A' && b[i] <= 'Z':
		b[i] = 'a'
	default: // '-' or '_' in the secret
		b[i] = 'a'
	}
	return string(b)
}

func TestTokenRoleFollowsCreatorMembership(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, ownerSess := e.signup(t, "owner@example.com")
	owner := e.principal(t, ownerSess)
	adminUser, adminSess := e.signup(t, "admin@example.com")
	if err := e.svc.st.grantMembership(bg, e.d.SQL(), owner.OrgID, adminUser.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SwitchOrg(bg, e.principal(t, adminSess), owner.OrgID, ""); err != nil {
		t.Fatal(err)
	}
	admin := e.principal(t, adminSess)
	mint := func(role Role) string {
		t.Helper()
		c, err := e.svc.CreateToken(bg, admin, TokenCreateRequest{Name: "t-" + string(role), Role: role}, "")
		if err != nil {
			t.Fatal(err)
		}
		return c.Token
	}
	adminTok, memberTok := mint(RoleAdmin), mint(RoleMember)
	role := func(tok string) (Role, error) {
		p, err := e.svc.tokenPrincipal(bg, tok)
		if err != nil {
			return "", err
		}
		return p.Role, nil
	}
	if r, err := role(adminTok); err != nil || r != RoleAdmin {
		t.Fatalf("admin token = %s, %v", r, err)
	}
	// Creator demoted to member: the admin token drops to member, the
	// member token stays member.
	e.d.Exec(bg, `UPDATE memberships SET role = 'member' WHERE org_id = ? AND user_id = ?`, owner.OrgID, adminUser.ID)
	if r, _ := role(adminTok); r != RoleMember {
		t.Fatalf("admin token after demotion = %s, want member", r)
	}
	if r, _ := role(memberTok); r != RoleMember {
		t.Fatalf("member token after demotion = %s", r)
	}
	// Promoted back: the token never exceeds its own stored role either.
	e.d.Exec(bg, `UPDATE memberships SET role = 'owner' WHERE org_id = ? AND user_id = ?`, owner.OrgID, adminUser.ID)
	if r, _ := role(memberTok); r != RoleMember {
		t.Fatalf("member token after creator promotion = %s, want member", r)
	}
	// Creator disabled: refused.
	e.d.Exec(bg, `UPDATE users SET disabled = 1 WHERE id = ?`, adminUser.ID)
	if _, err := role(adminTok); err != ErrInvalidToken {
		t.Fatalf("disabled creator's token = %v", err)
	}
	e.d.Exec(bg, `UPDATE users SET disabled = 0 WHERE id = ?`, adminUser.ID)
	if _, err := role(adminTok); err != nil {
		t.Fatalf("re-enabled creator's token = %v", err)
	}
	// Creator removed from the org: refused.
	e.d.Exec(bg, `DELETE FROM memberships WHERE org_id = ? AND user_id = ?`, owner.OrgID, adminUser.ID)
	if _, err := role(adminTok); err != ErrInvalidToken {
		t.Fatalf("removed creator's token = %v", err)
	}
	if _, err := role(memberTok); err != ErrInvalidToken {
		t.Fatalf("removed creator's member token = %v", err)
	}
	// Creator deleted: their tokens go with them (ON DELETE CASCADE).
	e.d.Exec(bg, `DELETE FROM users WHERE id = ?`, adminUser.ID)
	var n int
	e.d.QueryRow(bg, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, adminUser.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d tokens of a deleted user left", n)
	}
	// An org-level token (no creator) keeps its stored role.
	raw, prefix, _ := NewAPIToken()
	orgTok := &APIToken{ID: "tok_orglevel", OrgID: owner.OrgID, Name: "org", Prefix: prefix, Role: RoleMember, CreatedAt: e.clk.Now()}
	if err := e.svc.st.insertToken(bg, e.d.SQL(), orgTok, HashSecret(raw)); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.tokenPrincipal(bg, raw)
	if err != nil || p.Role != RoleMember || p.UserID != "" || p.OrgID != owner.OrgID {
		t.Fatalf("org-level token = %+v, %v", p, err)
	}
}
