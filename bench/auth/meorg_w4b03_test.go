package auth

import "testing"

// TestMeForFollowsRef: a user in orgs A (the session's default) and B asks
// for B by slug: /auth/me answers B with the role there and names A as the
// default (wave 4, organization per tab).
func TestMeForFollowsRef(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sa := e.signup(t, "a@example.com")
	_, sb := e.signup(t, "b@example.com")
	a, b := e.principal(t, sa), e.principal(t, sb)
	if err := e.svc.st.grantMembership(bg, e.d.SQL(), b.OrgID, a.UserID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	full, err := e.svc.Me(bg, a)
	if err != nil {
		t.Fatal(err)
	}
	slugB := ""
	for _, o := range full.Orgs {
		if o.ID == b.OrgID {
			slugB = o.Slug
		}
	}
	if slugB == "" {
		t.Fatalf("org B missing from %+v", full.Orgs)
	}
	me, err := e.svc.MeFor(bg, a, slugB)
	if err != nil || me.CurrentOrg != b.OrgID || me.Role != RoleViewer || me.DefaultOrg != a.OrgID || me.OrgReset {
		t.Fatalf("me for B = %+v, %v", me, err)
	}
}

// TestMeForUnknownRefResets: a ref the user cannot open keeps the session's
// org and says so with org_reset.
func TestMeForUnknownRefResets(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sa := e.signup(t, "a@example.com")
	_, sb := e.signup(t, "b@example.com")
	a, b := e.principal(t, sa), e.principal(t, sb)
	me, err := e.svc.MeFor(bg, a, b.OrgID)
	if err != nil || me.CurrentOrg != a.OrgID || !me.OrgReset || me.Role != RoleOwner {
		t.Fatalf("me for a foreign org = %+v, %v", me, err)
	}
}
