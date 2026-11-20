package server

import (
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// 4-A-18: every local request carries the local-org owner principal, as
// docs/auth.md promises, not only the synthetic /auth/me answer.
func TestLocalRequestsCarryLocalPrincipal(t *testing.T) {
	t.Setenv(lib.WriteTokenEnv, "tok")
	s, err := New(testConfig(), Deps{UI: testUI(), Version: "v-test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI("/api/bench/health")
	ctx.Request.SetHost("127.0.0.1:8090")
	s.handler(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("health = %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	p := auth.PrincipalFrom(ctx)
	if p == nil || p.Kind != auth.KindLocal || p.OrgID != auth.LocalOrgID || p.Role != auth.RoleOwner {
		t.Fatalf("local principal = %+v", p)
	}
}
