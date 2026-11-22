package tenancy

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestRouteGate: the installed gate sees the org and the router pattern,
// and a refusal stops the request; with no gate, or no org, all pass.
func TestRouteGate(t *testing.T) {
	t.Cleanup(func() { SetRouteGate(nil) })
	ctx := &fasthttp.RequestCtx{}
	o := &Org{ID: "org_a"}
	if !passGate(ctx, schemas.ModeLive, o, "POST", api+"/suites") {
		t.Fatal("no gate refused")
	}
	var seen string
	SetRouteGate(func(ctx *fasthttp.RequestCtx, _ schemas.Mode, orgID, method, pattern string) bool {
		seen = orgID + " " + method + " " + pattern
		if pattern == api+"/suites" {
			ctx.SetStatusCode(fasthttp.StatusPaymentRequired)
			return false
		}
		return true
	})
	if passGate(ctx, schemas.ModeLive, o, "POST", api+"/suites") || seen != "org_a POST "+api+"/suites" || ctx.Response.StatusCode() != 402 {
		t.Fatalf("gate: %q %d", seen, ctx.Response.StatusCode())
	}
	if !passGate(ctx, schemas.ModeLive, o, "GET", api+"/runs") {
		t.Fatal("other route refused")
	}
	if !passGate(ctx, schemas.ModeLive, nil, "POST", api+"/suites") {
		t.Fatal("a route with no org is not gated")
	}
	SetRouteGate(nil)
	if !passGate(ctx, schemas.ModeLive, o, "POST", api+"/suites") {
		t.Fatal("removed gate still refuses")
	}
}
