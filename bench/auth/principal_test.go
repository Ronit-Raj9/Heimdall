package auth

import (
	"context"
	"testing"

	"github.com/valyala/fasthttp"
)

func TestRoleOrdering(t *testing.T) {
	cases := []struct {
		r, min Role
		want   bool
	}{
		{RoleOwner, RoleViewer, true},
		{RoleOwner, RoleOwner, true},
		{RoleAdmin, RoleOwner, false},
		{RoleAdmin, RoleMember, true},
		{RoleMember, RoleAdmin, false},
		{RoleViewer, RoleMember, false},
		{RoleViewer, RoleViewer, true},
		{Role(""), RoleViewer, false},
		{Role("root"), RoleViewer, false},
		{RoleOwner, Role("typo"), false},
		{RoleOwner, Role(""), false},
	}
	for _, c := range cases {
		if got := c.r.AtLeast(c.min); got != c.want {
			t.Errorf("%q.AtLeast(%q) = %v, want %v", c.r, c.min, got, c.want)
		}
	}
	for i := 1; i < len(Roles); i++ {
		if Roles[i].Rank() <= Roles[i-1].Rank() {
			t.Fatalf("Roles not ascending at %d", i)
		}
	}
}

func TestParseRole(t *testing.T) {
	if r, err := ParseRole(" Admin "); err != nil || r != RoleAdmin {
		t.Fatalf("ParseRole admin = %q, %v", r, err)
	}
	if _, err := ParseRole("superuser"); err == nil {
		t.Fatal("ParseRole accepted superuser")
	}
}

func TestPrincipalOnRequestCtx(t *testing.T) {
	var rc fasthttp.RequestCtx
	if p := PrincipalFrom(&rc); p.Kind != KindAnonymous || p.Can(RoleViewer) {
		t.Fatalf("unset principal = %+v, want anonymous without role", p)
	}
	if _, ok := Lookup(&rc); ok {
		t.Fatal("Lookup found a principal on a fresh ctx")
	}
	// A user value of the same key but another type is not a principal.
	rc.SetUserValue(principalKey, &Principal{Kind: KindUser, Role: RoleOwner})
	if p := PrincipalFrom(&rc); p.Kind != KindAnonymous {
		t.Fatalf("forged user value accepted: %+v", p)
	}
	want := &Principal{Kind: KindUser, UserID: "usr_1", OrgID: "org_1", Role: RoleAdmin, Email: "a@x.test"}
	got := WithPrincipal(&rc, want)
	if got != context.Context(&rc) {
		t.Fatal("WithPrincipal on a RequestCtx should return the same ctx")
	}
	if p := PrincipalFrom(&rc); p != want {
		t.Fatalf("PrincipalFrom = %+v", p)
	}
	// A plain context derived from the RequestCtx still sees it.
	type otherKey struct{}
	derived := context.WithValue(&rc, otherKey{}, 1)
	if p := PrincipalFrom(derived); p != want {
		t.Fatalf("derived PrincipalFrom = %+v", p)
	}
}

func TestPrincipalOnPlainContext(t *testing.T) {
	p := &Principal{Kind: KindToken, TokenID: "tok_1", OrgID: "org_1", Role: RoleMember}
	ctx := WithPrincipal(context.Background(), p)
	if got := PrincipalFrom(ctx); got != p {
		t.Fatalf("PrincipalFrom = %+v", got)
	}
	if PrincipalFrom(context.Background()).Kind != KindAnonymous {
		t.Fatal("background ctx not anonymous")
	}
	if PrincipalFrom(nil).Kind != KindAnonymous { //nolint:staticcheck // nil ctx on purpose
		t.Fatal("nil ctx not anonymous")
	}
}

func TestPrincipalHelpers(t *testing.T) {
	l := Local()
	if l.OrgID != LocalOrgID || l.Role != RoleOwner || !l.Can(RoleOwner) || l.ActorType() != "system" {
		t.Fatalf("Local() = %+v", l)
	}
	a := Anonymous()
	if a.IsAuthenticated() || a.Can(RoleViewer) {
		t.Fatal("anonymous is authenticated")
	}
	// An anonymous principal given a role still can not act.
	a.Role = RoleOwner
	if a.Can(RoleViewer) {
		t.Fatal("anonymous with role can act")
	}
	u := &Principal{Kind: KindUser, UserID: "usr_1", Role: RoleMember}
	if u.ActorType() != "user" || u.ActorID() != "usr_1" {
		t.Fatalf("user actor = %s %s", u.ActorType(), u.ActorID())
	}
	tk := &Principal{Kind: KindToken, TokenID: "tok_1", UserID: "usr_1"}
	if tk.ActorType() != "token" || tk.ActorID() != "tok_1" {
		t.Fatalf("token actor = %s %s", tk.ActorType(), tk.ActorID())
	}
	var nilP *Principal
	if nilP.Can(RoleViewer) || nilP.IsAuthenticated() || nilP.SessionID() != "" {
		t.Fatal("nil principal grants something")
	}
}
