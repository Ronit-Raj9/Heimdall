package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// inviteMember adds email to owner's org as a member and returns the user.
func (e *testEnv) inviteMember(t *testing.T, owner *Principal, email string) *User {
	t.Helper()
	inv, err := e.svc.CreateInvitation(bg, owner, owner.OrgID, email, RoleMember, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := e.svc.Signup(bg, SignupRequest{Email: email, Password: goodPassword, Invitation: inv.Token}, "10.0.0.2", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestResetLinkWorksOnce(t *testing.T) {
	e := newTestEnv(t, SignupInvite)
	_, osess := e.signup(t, "owner@example.com")
	owner := e.principal(t, osess)
	m := e.inviteMember(t, owner, "member@example.com")

	link, err := e.svc.CreateResetLink(bg, owner, owner.OrgID, m.ID, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link.URL, "https://heimdall.example/reset-password?token=") || link.Email != "member@example.com" {
		t.Fatalf("link = %+v", link)
	}
	info, err := e.svc.LookupReset(bg, link.Token, "10.0.0.3")
	if err != nil || info.Email != "member@example.com" {
		t.Fatalf("lookup = %+v, %v", info, err)
	}
	// A weak password is refused and the link stays usable.
	if err := e.svc.ResetPassword(bg, link.Token, ResetRequest{Password: "short"}, "10.0.0.3"); err == nil {
		t.Fatal("weak password accepted")
	}
	const fresh = "orange-bicycle-lantern"
	if err := e.svc.ResetPassword(bg, link.Token, ResetRequest{Password: fresh}, "10.0.0.3"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ResetPassword(bg, link.Token, ResetRequest{Password: fresh + "x"}, "10.0.0.3"); !errors.Is(err, ErrInvalidReset) {
		t.Fatalf("second use = %v, want ErrInvalidReset", err)
	}
	if _, _, err := e.svc.Login(bg, LoginRequest{Email: "member@example.com", Password: fresh}, "10.0.0.3", "test", ""); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
	if e.auditCount(t, "auth.password_reset") != 1 || e.auditCount(t, "auth.reset_link_create") != 1 {
		t.Fatal("audit rows missing")
	}
}

func TestResetLinkRules(t *testing.T) {
	e := newTestEnv(t, SignupInvite)
	_, osess := e.signup(t, "owner@example.com")
	owner := e.principal(t, osess)
	m := e.inviteMember(t, owner, "member@example.com")

	// Not for yourself.
	if _, err := e.svc.CreateResetLink(bg, owner, owner.OrgID, owner.UserID, ""); err == nil {
		t.Fatal("self reset link allowed")
	}
	// A member (not owner) can not make links.
	_, msess, err := e.svc.Login(bg, LoginRequest{Email: "member@example.com", Password: goodPassword}, "10.0.0.4", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	mp := e.principal(t, msess)
	if _, err := e.svc.CreateResetLink(bg, mp, owner.OrgID, owner.UserID, ""); err == nil {
		t.Fatal("member made a reset link")
	}
	// Expired links fail.
	link, err := e.svc.CreateResetLink(bg, owner, owner.OrgID, m.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(ResetTTL + time.Minute)
	if _, err := e.svc.LookupReset(bg, link.Token, ""); !errors.Is(err, ErrInvalidReset) {
		t.Fatalf("expired lookup = %v", err)
	}
	// Unknown token.
	if err := e.svc.ResetPassword(bg, "nope", ResetRequest{Password: "orange-bicycle-lantern"}, ""); !errors.Is(err, ErrInvalidReset) {
		t.Fatalf("unknown token = %v", err)
	}
}

func TestSignupGlobalFromEnv(t *testing.T) {
	if got := SignupGlobalFromEnv(func(string) string { return "" }); got != SignupGlobal {
		t.Fatalf("default = %+v", got)
	}
	got := SignupGlobalFromEnv(func(string) string { return "6" })
	if got.Burst != 6 || got.Every != 10*time.Minute {
		t.Fatalf("6/hour = %+v", got)
	}
}

func TestSignupGlobalLimit429(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	e.svc.signupAll = NewLimiter(RateLimit{Burst: 2, Every: time.Hour}, e.clk.Now)
	for i, ip := range []string{"10.1.0.1", "10.1.0.2"} {
		if _, _, err := e.svc.Signup(bg, SignupRequest{Email: "u" + string(rune('a'+i)) + "@example.com", Password: goodPassword}, ip, "t", ""); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := e.svc.Signup(bg, SignupRequest{Email: "uz@example.com", Password: goodPassword}, "10.1.0.9", "t", "")
	if ae, ok := AsError(err); !ok || ae.Status != http.StatusTooManyRequests {
		t.Fatalf("third signup = %v, want 429", err)
	}
}

func TestSignupCaptcha(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("secret") != "s3cret" {
			t.Errorf("secret not sent")
		}
		if r.Form.Get("response") == "good" {
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":false}`))
	}))
	defer srv.Close()
	old := CaptchaVerifyURL
	CaptchaVerifyURL = srv.URL
	defer func() { CaptchaVerifyURL = old }()

	e := newTestEnv(t, SignupOpen)
	e.svc.captcha = &captchaConfig{secret: "s3cret", siteKey: "site"}
	if info := e.svc.Info(bg); !info.Captcha || info.CaptchaSiteKey != "site" {
		t.Fatalf("info = %+v", info)
	}
	if _, _, err := e.svc.Signup(bg, SignupRequest{Email: "a@example.com", Password: goodPassword}, "10.2.0.1", "t", ""); !errors.Is(err, ErrCaptcha) {
		t.Fatalf("missing captcha = %v", err)
	}
	if _, _, err := e.svc.Signup(bg, SignupRequest{Email: "a@example.com", Password: goodPassword, Captcha: "bad"}, "10.2.0.1", "t", ""); !errors.Is(err, ErrCaptcha) {
		t.Fatalf("bad captcha = %v", err)
	}
	if _, _, err := e.svc.Signup(bg, SignupRequest{Email: "a@example.com", Password: goodPassword, Captcha: "good"}, "10.2.0.1", "t", ""); err != nil {
		t.Fatalf("good captcha: %v", err)
	}
}
