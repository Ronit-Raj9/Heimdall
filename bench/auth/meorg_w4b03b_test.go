package auth

import "testing"

// TestMeForTokenIgnoresRef: an API token or a runner that names another org
// keeps its own org in /auth/me, with no org_reset and no other org listed
// (wave 4, organization per tab, group B review).
func TestMeForTokenIgnoresRef(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, sa := e.signup(t, "a@example.com")
	_, sb := e.signup(t, "b@example.com")
	a, b := e.principal(t, sa), e.principal(t, sb)
	for _, kind := range []PrincipalKind{KindToken, KindRunner} {
		p := &Principal{Kind: kind, TokenID: "tok_1", OrgID: a.OrgID, Role: RoleMember}
		me, err := e.svc.MeFor(bg, p, b.OrgID)
		if err != nil {
			t.Fatal(err)
		}
		if me.CurrentOrg != a.OrgID || me.OrgReset || me.DefaultOrg != "" {
			t.Fatalf("%s naming another org: %+v", kind, me)
		}
		for _, o := range me.Orgs {
			if o.ID == b.OrgID {
				t.Fatalf("%s sees the other org: %+v", kind, me.Orgs)
			}
		}
	}
}
