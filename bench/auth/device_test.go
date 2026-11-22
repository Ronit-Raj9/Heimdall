package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeviceLoginApproveMintsScopedToken(t *testing.T) {
	e := newTestEnv(t, SignupInvite)
	_, sess := e.signup(t, "owner@example.com")
	owner := e.principal(t, sess)
	f := NewDeviceFlow(e.svc)

	code, err := f.Start(DeviceCodeRequest{ClientName: "heimdall CLI on laptop\x07"}, "10.0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	if code.VerificationURI != "https://heimdall.example/device" || !strings.HasPrefix(code.VerificationURIComplete, "https://heimdall.example/device?code=") {
		t.Fatalf("verification links = %+v", code)
	}
	if len(code.UserCode) != 9 || code.UserCode[4] != '-' || code.Interval != 5 || code.ExpiresIn != 600 {
		t.Fatalf("code = %+v", code)
	}
	poll := DeviceTokenRequest{GrantType: DeviceGrantType, DeviceCode: code.DeviceCode}
	if _, derr := f.Poll(poll, "10.0.0.9"); derr == nil || derr.Code != DeviceErrPending {
		t.Fatalf("first poll = %v, want authorization_pending", derr)
	}
	// Polling again inside the interval slows the CLI down.
	if _, derr := f.Poll(poll, "10.0.0.9"); derr == nil || derr.Code != DeviceErrSlowDown || derr.Interval != 10 {
		t.Fatalf("fast poll = %+v, want slow_down 10", derr)
	}

	// The approval page finds it by the typed code, in any case and spacing.
	typed := strings.ToLower(strings.ReplaceAll(code.UserCode, "-", " "))
	info, err := f.Lookup(owner, typed)
	if err != nil || info.ClientName != "heimdall CLI on laptop" || info.Scope != ScopeEvaluate || info.Status != "pending" {
		t.Fatalf("lookup = %+v, %v", info, err)
	}
	ok, err := f.Approve(bg, owner, typed, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if ok.OrgID != owner.OrgID || ok.Role != RoleOwner || len(ok.Scopes) != 1 || ok.Scopes[0] != ScopeEvaluate {
		t.Fatalf("approved = %+v", ok)
	}
	if _, err := f.Approve(bg, owner, code.UserCode, "10.0.0.1"); err == nil {
		t.Fatal("second approval accepted")
	}

	e.clk.Advance(20 * time.Second)
	tok, derr := f.Poll(poll, "10.0.0.9")
	if derr != nil {
		t.Fatalf("poll after approval = %v", derr)
	}
	if !strings.HasPrefix(tok.AccessToken, APITokenPrefix) || tok.TokenType != "bearer" || tok.OrgID != owner.OrgID || tok.Scope != ScopeEvaluate {
		t.Fatalf("token = %+v", tok)
	}
	// The minted token is a real API token of the org, narrowed to evaluate.
	p, err := e.svc.tokenPrincipal(bg, tok.AccessToken)
	if err != nil || p.OrgID != owner.OrgID || !ScopesAllow(p.Scopes, ScopeEvaluate) || ScopesAllow(p.Scopes, ScopeAdmin) {
		t.Fatalf("token principal = %+v, %v", p, err)
	}
	list, err := e.svc.ListTokens(bg, owner)
	if err != nil || len(list) != 1 || !strings.HasPrefix(list[0].Name, "CLI login: heimdall CLI on laptop") || list[0].ExpiresAt == nil {
		t.Fatalf("tokens = %+v, %v", list, err)
	}
	// Handed over once.
	if _, derr := f.Poll(poll, "10.0.0.9"); derr == nil || derr.Code != DeviceErrInvalid {
		t.Fatalf("poll after hand-over = %v, want invalid_grant", derr)
	}
	if e.auditCount(t, "auth.device_approve") != 1 {
		t.Fatal("approval not audited")
	}
}

func TestDeviceLoginDenyExpireAndScopes(t *testing.T) {
	e := newTestEnv(t, SignupInvite)
	_, osess := e.signup(t, "owner@example.com")
	owner := e.principal(t, osess)
	f := NewDeviceFlow(e.svc)

	c1, _ := f.Start(DeviceCodeRequest{}, "10.0.0.9")
	if err := f.Deny(owner, c1.UserCode, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, derr := f.Poll(DeviceTokenRequest{GrantType: DeviceGrantType, DeviceCode: c1.DeviceCode}, "10.0.0.9"); derr == nil || derr.Code != DeviceErrDenied {
		t.Fatalf("denied poll = %v", derr)
	}

	c2, _ := f.Start(DeviceCodeRequest{}, "10.0.0.9")
	e.clk.Advance(DeviceCodeTTL + time.Second)
	if _, err := f.Lookup(owner, c2.UserCode); err == nil {
		t.Fatal("expired code found")
	}
	if _, derr := f.Poll(DeviceTokenRequest{GrantType: DeviceGrantType, DeviceCode: c2.DeviceCode}, "10.0.0.9"); derr == nil || derr.Code != DeviceErrExpired {
		t.Fatalf("expired poll = %v", derr)
	}

	if _, derr := f.Poll(DeviceTokenRequest{GrantType: "password", DeviceCode: "x"}, "10.0.0.9"); derr == nil || derr.Code != DeviceErrGrant {
		t.Fatalf("wrong grant = %v", derr)
	}
	if _, err := f.Start(DeviceCodeRequest{Scope: "root"}, "10.0.0.9"); err == nil {
		t.Fatal("unknown scope accepted")
	}

	// A member asking for admin scope is refused; a viewer only gets read.
	if _, _, err := deviceRoleAndScopes(RoleMember, ScopeAdmin); err == nil {
		t.Fatal("member got admin scope")
	}
	if r, s, err := deviceRoleAndScopes(RoleViewer, ScopeEvaluate); err != nil || r != RoleViewer || s[0] != ScopeRead {
		t.Fatalf("viewer = %v %v %v", r, s, err)
	}
	// Tokens can not approve: a browser session must.
	if _, err := f.Approve(bg, &Principal{Kind: KindToken, OrgID: owner.OrgID, Role: RoleOwner}, "BCDF-GHJK", ""); !errors.Is(err, ErrSessionRequired) {
		t.Fatalf("token approval = %v", err)
	}
}

func TestNormalizeUserCode(t *testing.T) {
	for in, want := range map[string]string{"bcdfghjk": "BCDF-GHJK", " BCDF-ghjk ": "BCDF-GHJK", "bc": "BC"} {
		if got := NormalizeUserCode(in); got != want {
			t.Errorf("NormalizeUserCode(%q) = %q, want %q", in, got, want)
		}
	}
	for i := 0; i < 50; i++ {
		c, err := newUserCode()
		if err != nil || len(c) != 9 || strings.ContainsAny(c, "AEIOUY") {
			t.Fatalf("user code %q, %v", c, err)
		}
	}
}
