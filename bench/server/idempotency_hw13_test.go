package server

import (
	"strings"
	"testing"

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
	// The hosted middleware runs inside the dispatcher, after the policy
	// check, so the request must reach an org the caller belongs to.
	f := newReplayFixture(t, false)
	ctx := f.post("sess-u1", "bad key")
	if ctx.Response.StatusCode() != 400 || !strings.Contains(string(ctx.Response.Body()), "Idempotency-Key must be 16 to 64") {
		t.Fatalf("hosted: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}
