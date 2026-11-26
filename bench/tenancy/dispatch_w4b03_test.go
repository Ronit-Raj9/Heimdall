package tenancy

import (
	"net"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestOrgQueryParamOnlyForReads: a GET naming an org in the heimdall_org
// query parameter is served in that org; a POST with the same parameter and
// no header acts in the session's org, so a planted link cannot write
// (wave 4, organization per tab).
func TestOrgQueryParamOnlyForReads(t *testing.T) {
	d, err := NewDispatcher(DispatcherConfig{
		Mode: schemas.ModeLive, Registry: newTestRegistry(t, &countingBuilder{}, Options{}),
		Dir: fakeDir{orgs: []Org{{ID: "org_a", Slug: "acme"}, {ID: "org_b", Slug: "beta"}},
			members: map[string]auth.Role{"org_a/usr_1": auth.RoleMember, "org_b/usr_1": auth.RoleMember}},
		Platform:  func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(fasthttp.StatusOK) },
		OrgRoutes: map[string][]string{"GET": {APIPrefix + "/agents"}, "POST": {APIPrefix + "/agents"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(method string) *fasthttp.RequestCtx {
		var ctx fasthttp.RequestCtx
		ctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1}, nil)
		ctx.Request.Header.SetMethod(method)
		ctx.Request.SetRequestURI(APIPrefix + "/agents?" + OrgQueryParam + "=beta")
		auth.SetPrincipal(&ctx, &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleMember})
		d.Handle(&ctx)
		return &ctx
	}
	if c := serve("GET"); !strings.HasPrefix(string(c.Response.Body()), "org_b|") {
		t.Fatalf("GET with the parameter served %d %q, want org_b", c.Response.StatusCode(), c.Response.Body())
	}
	if c := serve("POST"); !strings.HasPrefix(string(c.Response.Body()), "org_a|") {
		t.Fatalf("POST with only the parameter served %d %q, want the session's org_a", c.Response.StatusCode(), c.Response.Body())
	}
}
