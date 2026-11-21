package tenancy

import (
	"errors"
	"net"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func TestRequiredScope(t *testing.T) {
	pol := DefaultPolicy()
	rule := func(method, pattern string) Rule {
		r, ok := pol.OrgRule(method, pattern)
		if !ok {
			t.Fatalf("no rule %s %s", method, pattern)
		}
		return r
	}
	cases := []struct{ method, pattern, want string }{
		{"GET", api + "/runs/{id}", auth.ScopeRead},
		{"GET", api + "/evaluations", auth.ScopeRead},
		{"POST", api + "/evaluations", auth.ScopeEvaluate},
		{"POST", api + "/estimate", auth.ScopeEvaluate},
		{"POST", api + "/agents/{id}/versions", auth.ScopeEvaluate},
		{"PUT", api + "/agents/{id}/config", auth.ScopeAdmin},
	}
	for _, c := range cases {
		if got := RequiredScope(c.method, c.pattern, rule(c.method, c.pattern)); got != c.want {
			t.Errorf("%s %s = %s, want %s", c.method, c.pattern, got, c.want)
		}
	}
	// A read whose rule needs admin needs the admin scope.
	if got := RequiredScope("GET", api+"/x", Rule{Access: AccessOrg, Min: auth.RoleAdmin}); got != auth.ScopeAdmin {
		t.Errorf("admin read = %s", got)
	}
}

func TestCheckScope(t *testing.T) {
	member := Rule{Access: AccessOrg, Min: auth.RoleMember}
	viewer := Rule{Access: AccessOrg, Min: auth.RoleViewer}
	read := &auth.Principal{Kind: auth.KindToken, Role: auth.RoleAdmin, Scopes: []string{auth.ScopeRead}}
	eval := &auth.Principal{Kind: auth.KindToken, Role: auth.RoleAdmin, Scopes: []string{auth.ScopeEvaluate}}
	legacy := &auth.Principal{Kind: auth.KindToken, Role: auth.RoleAdmin}
	user := &auth.Principal{Kind: auth.KindUser, Role: auth.RoleAdmin}
	var den *Denied
	if err := CheckScope(read, "GET", api+"/runs/{id}", viewer); err != nil {
		t.Errorf("read token read: %v", err)
	}
	if err := CheckScope(read, "POST", api+"/evaluations", member); !errors.As(err, &den) || den.Status != 403 {
		t.Errorf("read token evaluate: %v", err)
	}
	if err := CheckScope(eval, "POST", api+"/evaluations", member); err != nil {
		t.Errorf("evaluate token evaluate: %v", err)
	}
	if err := CheckScope(eval, "PUT", api+"/agents/{id}/config", member); err == nil {
		t.Error("evaluate token changed an agent config")
	}
	for _, p := range []*auth.Principal{legacy, user, nil} {
		if err := CheckScope(p, "PUT", api+"/agents/{id}/config", member); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
	}
}

// TestDispatcherChecksScopes: the dispatcher refuses a token whose scopes
// do not cover the route, after the role check passed.
func TestDispatcherChecksScopes(t *testing.T) {
	served := 0
	pol := &Policy{Platform: map[string]Rule{
		key("GET", APIPrefix+"/thing"):  signedIn(),
		key("POST", APIPrefix+"/thing"): signedIn(),
	}, Org: map[string]Rule{}}
	d, err := NewDispatcher(DispatcherConfig{
		Mode: schemas.ModeLive, Policy: pol, Dir: noDir{}, Registry: newTestRegistry(t, &countingBuilder{}, Options{}),
		Platform:       func(ctx *fasthttp.RequestCtx) { served++ },
		PlatformRoutes: map[string][]string{"GET": {APIPrefix + "/thing"}, "POST": {APIPrefix + "/thing"}},
		OrgRoutes:      map[string][]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method string, scopes []string) int {
		var ctx fasthttp.RequestCtx
		ctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1}, nil)
		ctx.Request.Header.SetMethod(method)
		ctx.Request.SetRequestURI(APIPrefix + "/thing")
		auth.SetPrincipal(&ctx, &auth.Principal{Kind: auth.KindToken, TokenID: "t", OrgID: "org_a", Role: auth.RoleAdmin, Scopes: scopes})
		d.Handle(&ctx)
		return ctx.Response.StatusCode()
	}
	if s := do("GET", []string{auth.ScopeRead}); s != 200 || served != 1 {
		t.Fatalf("read GET = %d served %d", s, served)
	}
	if s := do("POST", []string{auth.ScopeRead}); s != 403 || served != 1 {
		t.Fatalf("read POST = %d served %d", s, served)
	}
	if s := do("POST", []string{auth.ScopeAdmin}); s != 200 || served != 2 {
		t.Fatalf("admin POST = %d served %d", s, served)
	}
}
