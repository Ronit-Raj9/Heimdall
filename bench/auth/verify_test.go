package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/schemas"
)

// logSink captures Info lines (where links go with no mail sender).
type logSink struct {
	schemas.NoopLogger
	mu    sync.Mutex
	lines []string
}

func (l *logSink) Info(msg string, _ ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, msg)
	l.mu.Unlock()
}

var linkRe = regexp.MustCompile(`https://heimdall\.example/verify-email\?token=([A-Za-z0-9_%-]+)`)

// lastToken is the token of the last link in s.
func lastToken(t *testing.T, s string) string {
	t.Helper()
	m := linkRe.FindAllStringSubmatch(s, -1)
	if len(m) == 0 {
		t.Fatalf("no verification link in %q", s)
	}
	tok, err := url.QueryUnescape(m[len(m)-1][1])
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func newVerifyEnv(t *testing.T, signup SignupPolicy, domains []string, send MailSender) (*testEnv, *logSink) {
	t.Helper()
	d := dbtest.Open(t)
	clk := newFakeClock()
	logs := &logSink{}
	cfg := Config{Hosted: true, ServerMode: schemas.ModeLive, PublicURL: "https://heimdall.example", Signup: signup, SignupDomains: domains,
		SessionTTL: 48 * time.Hour, SessionIdle: 6 * time.Hour, InviteTTL: 72 * time.Hour, AllowedOrigins: []string{"https://heimdall.example"}}
	svc, err := New(cfg, Deps{DB: d, Hasher: NewHasher(fastParams), Now: clk.Now, Logger: logs, SendMail: send})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{svc: svc, d: d, clk: clk}, logs
}

func setAdmins(t *testing.T, emails, ids string) {
	t.Helper()
	t.Setenv(ServerAdminsEnv, emails)
	t.Setenv(ServerAdminIDsEnv, ids)
	resetServerAdminsForTest()
	t.Cleanup(resetServerAdminsForTest)
}

// TestUnverifiedAccountLimits: a new password account is unverified; it can
// sign in and use its org but can not make API tokens, invite or create
// orgs until it opens the link (logged when no mail sender is set).
func TestUnverifiedAccountLimits(t *testing.T) {
	setAdmins(t, "", "")
	e, logs := newVerifyEnv(t, SignupOpen, nil, nil)
	_, sess := e.signupUnverified(t, "ana@example.com")
	p := e.principal(t, sess)
	if p.EmailVerified {
		t.Fatal("new password account verified")
	}
	me, err := e.svc.Me(bg, p)
	if err != nil || me.EmailVerified == nil || *me.EmailVerified {
		t.Fatalf("me = %+v %v", me, err)
	}
	if _, err := e.svc.CreateToken(bg, p, TokenCreateRequest{Name: "ci"}, ""); !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("token by unverified user = %v", err)
	}
	if _, err := e.svc.CreateInvitation(bg, p, p.OrgID, "bo@example.com", RoleMember, ""); !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("invite by unverified user = %v", err)
	}
	if err := RequireVerified(p); !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("RequireVerified = %v", err)
	}
	if RequireVerified(&Principal{Kind: KindToken}) != nil || RequireVerified(Local()) != nil {
		t.Fatal("tokens and local mode need no verification")
	}
	if e.svc.Info(bg).EmailDelivery != DeliveryLog {
		t.Fatal("delivery should be log with no sender")
	}
	logs.mu.Lock()
	all := strings.Join(logs.lines, "\n")
	logs.mu.Unlock()
	if !strings.Contains(all, "ana@example.com") || !strings.Contains(all, "no mail sender") {
		t.Fatalf("link not logged: %q", all)
	}
	res, err := e.svc.VerifyEmail(bg, VerifyRequest{Token: lastToken(t, all)}, "10.0.0.9")
	if err != nil || !res.Verified || res.Email != "ana@example.com" {
		t.Fatalf("verify = %+v %v", res, err)
	}
	p = e.principal(t, sess)
	if !p.EmailVerified {
		t.Fatal("not verified after the link")
	}
	if _, err := e.svc.CreateToken(bg, p, TokenCreateRequest{Name: "ci"}, ""); err != nil {
		t.Fatalf("token after verify = %v", err)
	}
	if _, err := e.svc.CreateInvitation(bg, p, p.OrgID, "bo@example.com", RoleMember, ""); err != nil {
		t.Fatalf("invite after verify = %v", err)
	}
	if _, err := e.svc.VerifyEmail(bg, VerifyRequest{Token: lastToken(t, all)}, "10.0.0.9"); !errors.Is(err, ErrInvalidVerification) {
		t.Fatalf("reused link = %v", err)
	}
	sent, err := e.svc.SendVerification(bg, p, "")
	if err != nil || !sent.AlreadyVerified {
		t.Fatalf("resend when verified = %+v %v", sent, err)
	}
}

// TestVerificationLinks: links are mailed when a sender is set, a resend
// replaces the pending link, links expire, resends are paced, and a link
// made for an older address does not verify a new one.
func TestVerificationLinks(t *testing.T) {
	setAdmins(t, "", "")
	var mu sync.Mutex
	var mails []string
	send := func(_ context.Context, to, subject, text, html string) error {
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(html, "verify-email?token=") || subject == "" {
			return fmt.Errorf("bad mail")
		}
		mails = append(mails, to+"\n"+text)
		return nil
	}
	e, logs := newVerifyEnv(t, SignupOpen, nil, send)
	u, sess := e.signupUnverified(t, "cy@example.com")
	_ = sess
	p := e.principal(t, sess)
	if len(mails) != 1 || !strings.HasPrefix(mails[0], "cy@example.com\n") {
		t.Fatalf("signup mail %q", mails)
	}
	for _, l := range logs.lines {
		if strings.Contains(l, "verify-email?token=") {
			t.Fatalf("link logged although mailed: %q", l)
		}
	}
	first := lastToken(t, mails[0])
	sent, err := e.svc.SendVerification(bg, p, "")
	if err != nil || sent.Delivery != DeliveryEmail || sent.Email != "cy@example.com" {
		t.Fatalf("resend = %+v %v", sent, err)
	}
	second := lastToken(t, mails[1])
	if _, err := e.svc.VerifyEmail(bg, VerifyRequest{Token: first}, ""); !errors.Is(err, ErrInvalidVerification) {
		t.Fatalf("replaced link = %v", err)
	}
	// Paced: signup used one, resend one, one more is allowed, then 429.
	if _, err := e.svc.SendVerification(bg, p, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SendVerification(bg, p, ""); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("unpaced resend = %v", err)
	}
	third := lastToken(t, mails[2])
	if third == second {
		t.Fatal("resend reused the token")
	}
	e.clk.Advance(VerifyTTL + time.Minute)
	if _, err := e.svc.VerifyEmail(bg, VerifyRequest{Token: third}, ""); !errors.Is(err, ErrInvalidVerification) {
		t.Fatalf("expired link = %v", err)
	}
	// A fresh link for the old address stops working once the email changes.
	if _, err := e.svc.SendVerification(bg, p, ""); err != nil {
		t.Fatal(err)
	}
	fresh := lastToken(t, mails[len(mails)-1])
	if _, err := e.d.SQL().Exec(`UPDATE users SET email = 'cy2@example.com' WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.VerifyEmail(bg, VerifyRequest{Token: fresh}, ""); !errors.Is(err, ErrInvalidVerification) {
		t.Fatalf("link for an old address = %v", err)
	}
	if _, err := e.svc.VerifyEmail(bg, VerifyRequest{Token: ""}, ""); !errors.Is(err, ErrInvalidVerification) {
		t.Fatalf("empty token = %v", err)
	}
	// A verification of the old address does not carry over to the new one.
	if err := e.svc.st.markVerified(bg, e.d.SQL(), u.ID, "cy@example.com", VerifiedByEmail); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.svc.st.emailVerified(bg, e.d.SQL(), &User{ID: u.ID, Email: "cy2@example.com"}); err != nil || ok {
		t.Fatalf("verification of an old address counted: %v %v", ok, err)
	}
}

// TestFailedMailFallsBackToLog: a sender error still logs the link for the
// operator and tells the caller.
func TestFailedMailFallsBackToLog(t *testing.T) {
	setAdmins(t, "", "")
	e, logs := newVerifyEnv(t, SignupOpen, nil, func(context.Context, string, string, string, string) error {
		return errors.New("smtp down")
	})
	_, sess := e.signupUnverified(t, "dee@example.com")
	p := e.principal(t, sess)
	sent, err := e.svc.SendVerification(bg, p, "")
	if err == nil || sent == nil || sent.Delivery != DeliveryLog {
		t.Fatalf("failed send = %+v %v", sent, err)
	}
	if !strings.Contains(strings.Join(logs.lines, "\n"), "smtp down") {
		t.Fatalf("logs %q", logs.lines)
	}
}

// TestServerAdminNeedsVerifiedEmail: a listed email never makes an
// unverified account a server admin (audit B1); verifying binds it by id;
// unlisting ends it; CLI grants and listed ids work by id.
func TestServerAdminNeedsVerifiedEmail(t *testing.T) {
	setAdmins(t, "root@example.com", "")
	e, logs := newVerifyEnv(t, SignupOpen, nil, nil)
	// The squatter: registers the listed address with a password.
	_, sess := e.signupUnverified(t, "root@example.com")
	p := e.principal(t, sess)
	if IsServerAdmin(p) {
		t.Fatal("unverified listed email is a server admin")
	}
	if me, _ := e.svc.Me(bg, p); me.ServerAdmin {
		t.Fatal("me says server admin for an unverified email")
	}
	if IsServerAdmin(&Principal{Kind: KindUser, UserID: p.UserID, Email: "root@example.com"}) {
		t.Fatal("email match alone grants server admin")
	}
	if list, err := ServerAdmins(bg, e.d); err != nil || len(list) != 0 {
		t.Fatalf("admins before verify = %+v %v", list, err)
	}
	// The real owner verifies: the next request binds the address.
	if _, err := e.svc.VerifyEmail(bg, VerifyRequest{Token: lastToken(t, strings.Join(logs.lines, "\n"))}, ""); err != nil {
		t.Fatal(err)
	}
	p = e.principal(t, sess)
	if !IsServerAdmin(p) {
		t.Fatal("verified listed email is not a server admin")
	}
	g, err := e.svc.st.grantFor(bg, e.d.SQL(), p.UserID)
	if err != nil || g == nil || g.source != GrantEmail {
		t.Fatalf("binding = %+v %v", g, err)
	}
	list, err := ServerAdmins(bg, e.d)
	if err != nil || len(list) != 1 || list[0].Source != GrantEmail || !list[0].Verified {
		t.Fatalf("admins = %+v %v", list, err)
	}
	// Unlisted: the bound grant stops applying.
	setAdmins(t, "", "")
	if IsServerAdmin(e.principal(t, sess)) {
		t.Fatal("unlisted address still a server admin")
	}
	// A CLI grant works by id, verified or not.
	_, other := e.signupUnverified(t, "ops@example.com")
	if _, err := GrantServerAdmin(bg, e.d, "ops@example.com"); err != nil {
		t.Fatal(err)
	}
	op := e.principal(t, other)
	if op.EmailVerified || !IsServerAdmin(op) {
		t.Fatalf("cli grant: verified %v admin %v", op.EmailVerified, IsServerAdmin(op))
	}
	if _, had, err := RevokeServerAdmin(bg, e.d, "ops@example.com"); err != nil || !had {
		t.Fatalf("revoke = %v %v", had, err)
	}
	if IsServerAdmin(e.principal(t, other)) {
		t.Fatal("revoked grant still applies")
	}
	if _, err := GrantServerAdmin(bg, e.d, "nobody@example.com"); err == nil {
		t.Fatal("grant for a missing account")
	}
	// Listed by id.
	setAdmins(t, "", op.UserID)
	if !IsServerAdmin(e.principal(t, other)) {
		t.Fatal("listed id is not a server admin")
	}
	if IsServerAdmin(&Principal{Kind: KindToken, UserID: op.UserID}) {
		t.Fatal("a token is a server admin")
	}
}

// TestGitHubAndOperatorAccountsVerified: GitHub-linked accounts and accounts
// an operator made or marked count as verified.
func TestGitHubAndOperatorAccountsVerified(t *testing.T) {
	setAdmins(t, "", "")
	e, _ := newVerifyEnv(t, SignupInvite, nil, nil)
	gh, err := e.svc.st.insertUser(bg, e.d.SQL(), "gh@example.com", "", "", "12345")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := e.svc.st.emailVerified(bg, e.d.SQL(), gh); err != nil || !ok {
		t.Fatalf("github user verified = %v %v", ok, err)
	}
	c, err := CreateUser(bg, e.d, NewHasher(fastParams), "cli@example.com", "", goodPassword, "Acme", RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.svc.st.emailVerified(bg, e.d.SQL(), c.User); !ok {
		t.Fatal("cli-made account not verified")
	}
	pw, err := e.svc.st.insertUser(bg, e.d.SQL(), "pw@example.com", "", "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.svc.st.emailVerified(bg, e.d.SQL(), pw); ok {
		t.Fatal("plain password account verified")
	}
	if _, err := MarkEmailVerified(bg, e.d, "PW@example.com"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.svc.st.emailVerified(bg, e.d.SQL(), pw); !ok {
		t.Fatal("operator verify did not stick")
	}
	if _, err := MarkEmailVerified(bg, e.d, "missing@example.com"); err == nil {
		t.Fatal("verify of a missing account")
	}
}

// TestSignupModes: invite refuses self-serve signup, allowlist admits only
// the listed domains, and a server admin's override applies at once and
// can be reset to the config.
func TestSignupModes(t *testing.T) {
	setAdmins(t, "", "")
	e, _ := newVerifyEnv(t, SignupAllowlist, []string{"acme.com"}, nil)
	signup := func(email string) error {
		_, _, err := e.svc.Signup(bg, SignupRequest{Email: email, Password: goodPassword}, "10.1.0."+fmt.Sprint(len(email)), "", "")
		return err
	}
	if err := signup("first@elsewhere.org"); err != nil {
		t.Fatalf("bootstrap = %v", err)
	}
	if err := signup("eve@evil.com"); !errors.Is(err, ErrSignupDomain) {
		t.Fatalf("other domain = %v", err)
	}
	if err := signup("ann@acme.com"); err != nil {
		t.Fatalf("allowed domain = %v", err)
	}
	if err := signup("sub@eu.acme.com"); !errors.Is(err, ErrSignupDomain) {
		t.Fatalf("subdomain (exact match only) = %v", err)
	}
	info := e.svc.Info(bg)
	if info.Signup != "allowlist" || len(info.SignupDomains) != 1 || info.SignupDomains[0] != "acme.com" {
		t.Fatalf("info = %+v", info)
	}
	// Non-admins can not change the mode.
	plain := &Principal{Kind: KindUser, UserID: "usr_x", EmailVerified: true}
	if _, err := e.svc.SetSignupSettings(bg, plain, SignupSettingsRequest{Mode: "open"}, ""); err == nil {
		t.Fatal("non-admin changed the signup mode")
	}
	setAdmins(t, "", "usr_admin")
	admin := &Principal{Kind: KindUser, UserID: "usr_admin", EmailVerified: true}
	for _, bad := range []SignupSettingsRequest{{Mode: "everyone"}, {Mode: "allowlist"}, {Mode: "allowlist", Domains: []string{"not a domain"}}} {
		if _, err := e.svc.SetSignupSettings(bg, admin, bad, ""); err == nil {
			t.Fatalf("bad request %+v accepted", bad)
		}
	}
	got, err := e.svc.SetSignupSettings(bg, admin, SignupSettingsRequest{Mode: "invite"}, "")
	if err != nil || got.Mode != SignupInvite || got.Source != "admin" || got.ConfigMode != SignupAllowlist || got.UpdatedBy != "usr_admin" {
		t.Fatalf("override = %+v %v", got, err)
	}
	if err := signup("bob@acme.com"); !errors.Is(err, ErrSignupNotAllowed) {
		t.Fatalf("invite override = %v", err)
	}
	got, err = e.svc.SetSignupSettings(bg, admin, SignupSettingsRequest{Mode: "allowlist", Domains: []string{" @Beta.IO ", "beta.io", "acme.com"}}, "")
	if err != nil || strings.Join(got.Domains, ",") != "acme.com,beta.io" {
		t.Fatalf("domains = %+v %v", got, err)
	}
	if err := signup("cat@beta.io"); err != nil {
		t.Fatalf("new allowed domain = %v", err)
	}
	// Another replica (a second service on the same database) sees the
	// override after its reload interval.
	other, err := New(e.svc.cfg, Deps{DB: e.d, Hasher: NewHasher(fastParams), Now: e.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if m, d := other.signupPolicy(bg); m != SignupAllowlist || len(d) != 2 {
		t.Fatalf("replica sees %s %v", m, d)
	}
	got, err = e.svc.SetSignupSettings(bg, admin, SignupSettingsRequest{Mode: ""}, "")
	if err != nil || got.Source != "config" || got.Mode != SignupAllowlist || strings.Join(got.Domains, ",") != "acme.com" {
		t.Fatalf("reset = %+v %v", got, err)
	}
	if err := signup("dan@beta.io"); !errors.Is(err, ErrSignupDomain) {
		t.Fatalf("after reset = %v", err)
	}
	if !selfServeAllowed(SignupOpen, nil, "x@y.z") || selfServeAllowed(SignupInvite, []string{"y.z"}, "x@y.z") || selfServeAllowed(SignupClosed, nil, "x@y.z") {
		t.Fatal("selfServeAllowed")
	}
}
