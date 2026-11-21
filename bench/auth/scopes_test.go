package auth

import (
	"slices"
	"testing"
)

func TestScopesAllow(t *testing.T) {
	cases := []struct {
		have []string
		need string
		ok   bool
	}{
		{[]string{ScopeRead}, ScopeRead, true},
		{[]string{ScopeRead}, ScopeEvaluate, false},
		{[]string{ScopeEvaluate}, ScopeRead, true},
		{[]string{ScopeEvaluate}, ScopeAdmin, false},
		{[]string{ScopeAdmin}, ScopeEvaluate, true},
		{[]string{"bogus"}, ScopeRead, false},
		{nil, ScopeRead, false},
		{[]string{ScopeAdmin}, "bogus", false},
	}
	for _, c := range cases {
		if got := ScopesAllow(c.have, c.need); got != c.ok {
			t.Errorf("ScopesAllow(%v, %s) = %v", c.have, c.need, got)
		}
	}
	if got, ok := NormalizeScopes([]string{" Admin", "read", "read", ""}); !ok || !slices.Equal(got, []string{ScopeRead, ScopeAdmin}) {
		t.Errorf("normalize = %v %v", got, ok)
	}
	if _, ok := NormalizeScopes([]string{"write"}); ok {
		t.Error("unknown scope accepted")
	}
	// A stored value with only unknown entries stays restricted.
	if got := SplitScopes("bogus"); len(got) != 1 || ScopesAllow(got, ScopeRead) {
		t.Errorf("split bogus = %v", got)
	}
	if got := SplitScopes(""); got != nil {
		t.Errorf("split empty = %v", got)
	}
}

func TestAPITokenScopes(t *testing.T) {
	e := newTestEnv(t, SignupOpen)
	_, ownerSess := e.signup(t, "owner@example.com")
	owner := e.principal(t, ownerSess)
	if _, err := e.svc.CreateToken(bg, owner, TokenCreateRequest{Name: "x", Scopes: []string{"write"}}, ""); err == nil {
		t.Fatal("unknown scope accepted")
	}
	created, err := e.svc.CreateToken(bg, owner, TokenCreateRequest{Name: "ci", Scopes: []string{"evaluate", "read"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(created.APIToken.Scopes, []string{ScopeRead, ScopeEvaluate}) {
		t.Fatalf("scopes = %v", created.APIToken.Scopes)
	}
	tp, err := e.svc.tokenPrincipal(bg, created.Token)
	if err != nil || !slices.Equal(tp.Scopes, []string{ScopeRead, ScopeEvaluate}) {
		t.Fatalf("principal = %+v %v", tp, err)
	}
	list, err := e.svc.ListTokens(bg, owner)
	if err != nil || len(list) != 1 || !slices.Equal(list[0].Scopes, []string{ScopeRead, ScopeEvaluate}) {
		t.Fatalf("list = %+v %v", list, err)
	}
	// No scopes asked: admin, the role alone limits the token.
	full, err := e.svc.CreateToken(bg, owner, TokenCreateRequest{Name: "full"}, "")
	if err != nil || !slices.Equal(full.APIToken.Scopes, []string{ScopeAdmin}) {
		t.Fatalf("default scopes = %+v %v", full, err)
	}
}
