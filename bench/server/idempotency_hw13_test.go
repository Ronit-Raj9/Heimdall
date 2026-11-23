package server

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

func postWithKey(h fasthttp.RequestHandler, host, key string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI("/api/bench/evaluations")
	ctx.Request.Header.SetHost(host)
	ctx.Request.Header.SetContentType("application/json")
	ctx.Request.Header.Set("Idempotency-Key", key)
	ctx.Request.SetBodyString(`{}`)
	h(ctx)
	return ctx
}

// Both server chains honour Idempotency-Key (fix wave 3, N186): a malformed
// key is refused by the middleware before any route runs.
func TestIdempotencyMiddlewareInLocalChain(t *testing.T) {
	s, err := New(testConfig(), Deps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := postWithKey(s.Handler(), "127.0.0.1:8080", "bad key")
	if ctx.Response.StatusCode() != 400 || !strings.Contains(string(ctx.Response.Body()), "Idempotency-Key must be 16 to 64") {
		t.Fatalf("local: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

func TestIdempotencyMiddlewareInHostedChain(t *testing.T) {
	cfg := testConfig()
	cfg.Auth = lib.AuthConfig{Mode: lib.AuthModeHosted, PublicURL: "https://heimdall.test"}
	reg := tenancy.NewRegistry(tenancy.Layout{Hosted: true, StorageRoot: t.TempDir()}, nil, tenancy.Options{})
	signIn := func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			auth.SetPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, UserID: "u1", OrgID: "org1", Role: auth.RoleOwner})
			h(ctx)
		}
	}
	s, err := NewHosted(cfg, Hosted{Auth: signIn, Directory: noOrgs{}, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	defer s.closeHandlers()
	ctx := postWithKey(s.Handler(), "heimdall.test", "bad key")
	if ctx.Response.StatusCode() != 400 || !strings.Contains(string(ctx.Response.Body()), "Idempotency-Key must be 16 to 64") {
		t.Fatalf("hosted: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}
