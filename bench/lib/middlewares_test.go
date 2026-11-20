package lib

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// okHandler answers 200 "ok".
func okHandler(ctx *fasthttp.RequestCtx) { ctx.SetBodyString("ok") }

// run calls h with a request built by setup and returns the response status.
func run(h fasthttp.RequestHandler, setup func(r *fasthttp.Request)) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI("/api/bench/x")
	ctx.Request.Header.SetHost("127.0.0.1:8090")
	if setup != nil {
		setup(&ctx.Request)
	}
	h(ctx)
	return ctx
}

func TestChainMiddlewaresOrder(t *testing.T) {
	var order []string
	mw := func(name string) Middleware {
		return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
			return func(ctx *fasthttp.RequestCtx) { order = append(order, name); next(ctx) }
		}
	}
	run(ChainMiddlewares(okHandler, mw("a"), nil, mw("b")), nil)
	if strings.Join(order, ",") != "a,b" {
		t.Fatalf("order = %v", order)
	}
}

func TestRequireWriteToken(t *testing.T) {
	tests := []struct {
		name, header, token string
		status              int
	}{
		{"valid", "Bearer s3cret", "s3cret", 200},
		{"missing", "", "s3cret", 401},
		{"wrong", "Bearer nope", "s3cret", 401},
		{"no scheme", "s3cret", "s3cret", 401},
		{"empty configured token never matches", "Bearer ", "", 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := run(RequireWriteToken(schemas.ModeLive, tt.token)(okHandler), func(r *fasthttp.Request) {
				if tt.header != "" {
					r.Header.Set("Authorization", tt.header)
				}
			})
			if ctx.Response.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d", ctx.Response.StatusCode(), tt.status)
			}
		})
	}
}

func TestResolveWriteToken(t *testing.T) {
	t.Setenv(WriteTokenEnv, "")
	a, gen, err := ResolveWriteToken()
	if err != nil || !gen || len(a) != 64 {
		t.Fatalf("generated token %q %v %v", a, gen, err)
	}
	b, _, _ := ResolveWriteToken()
	if a == b {
		t.Error("generated tokens repeat")
	}
	t.Setenv(WriteTokenEnv, " fixed ")
	if tok, gen, err := ResolveWriteToken(); tok != "fixed" || gen || err != nil {
		t.Fatalf("env token %q %v %v", tok, gen, err)
	}
}

func TestBodyLimit(t *testing.T) {
	h := BodyLimit(schemas.ModeLive, 10)(okHandler)
	if got := run(h, func(r *fasthttp.Request) { r.SetBodyString("0123456789") }).Response.StatusCode(); got != 200 {
		t.Errorf("at limit = %d", got)
	}
	if got := run(h, func(r *fasthttp.Request) { r.SetBodyString("0123456789a") }).Response.StatusCode(); got != 413 {
		t.Errorf("over limit = %d", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	ctx := run(SecurityHeaders()(okHandler), nil)
	h := &ctx.Response.Header
	if csp := string(h.Peek("Content-Security-Policy")); !strings.HasPrefix(csp, "default-src 'self'") || !strings.Contains(csp, "script-src 'self';") {
		t.Errorf("csp = %q", csp)
	}
	for _, k := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if len(h.Peek(k)) == 0 {
			t.Errorf("missing %s", k)
		}
	}
	if strings.Contains(string(h.Peek("Content-Security-Policy")), "sandbox") {
		t.Errorf("plain response got a sandbox")
	}
}

func TestSecurityHeadersKeepsSandbox(t *testing.T) {
	sandboxed := func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Content-Security-Policy", "sandbox")
		ctx.SetStatusCode(200)
	}
	ctx := run(SecurityHeaders()(sandboxed), nil)
	csp := string(ctx.Response.Header.Peek("Content-Security-Policy"))
	if !strings.HasPrefix(csp, ContentSecurityPolicy) || !strings.HasSuffix(csp, "; sandbox") {
		t.Errorf("csp = %q", csp)
	}
}

func TestHostGuard(t *testing.T) {
	h := HostGuard(schemas.ModeLive, []string{"bench.internal"})(okHandler)
	for host, want := range map[string]int{"127.0.0.1:8090": 200, "localhost": 200, "[::1]:8090": 200,
		"bench.internal:8090": 200, "evil.example:8090": 403} {
		ctx := run(h, func(r *fasthttp.Request) { r.Header.SetHost(host) })
		if got := ctx.Response.StatusCode(); got != want {
			t.Errorf("host %s = %d, want %d", host, got, want)
		}
	}
}

func TestCORS(t *testing.T) {
	off := run(CORS(nil)(okHandler), func(r *fasthttp.Request) { r.Header.Set("Origin", "http://x") })
	if len(off.Response.Header.Peek(fasthttp.HeaderAccessControlAllowOrigin)) != 0 {
		t.Error("CORS must be off by default")
	}
	h := CORS([]string{"http://ui.local"})(okHandler)
	on := run(h, func(r *fasthttp.Request) { r.Header.Set("Origin", "http://ui.local") })
	if string(on.Response.Header.Peek(fasthttp.HeaderAccessControlAllowOrigin)) != "http://ui.local" {
		t.Error("listed origin not allowed")
	}
	// The hosted client sends credentials: "include"; a listed origin must
	// be told credentials are allowed or the browser drops the response.
	if string(on.Response.Header.Peek(fasthttp.HeaderAccessControlAllowCredentials)) != "true" {
		t.Error("listed origin lacks Access-Control-Allow-Credentials")
	}
	other := run(h, func(r *fasthttp.Request) { r.Header.Set("Origin", "http://evil") })
	if len(other.Response.Header.Peek(fasthttp.HeaderAccessControlAllowOrigin)) != 0 {
		t.Error("unlisted origin allowed")
	}
	pre := run(h, func(r *fasthttp.Request) {
		r.Header.SetMethod("OPTIONS")
		r.Header.Set("Origin", "http://ui.local")
	})
	if pre.Response.StatusCode() != fasthttp.StatusNoContent {
		t.Errorf("preflight = %d", pre.Response.StatusCode())
	}
	if string(pre.Response.Header.Peek(fasthttp.HeaderAccessControlAllowCredentials)) != "true" {
		t.Error("preflight lacks Access-Control-Allow-Credentials")
	}
	if len(other.Response.Header.Peek(fasthttp.HeaderAccessControlAllowCredentials)) != 0 {
		t.Error("unlisted origin got Access-Control-Allow-Credentials")
	}
}

func TestRequestErrorHandler(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	RequestErrorHandler(schemas.ModeLive, MaxBodyBytes)(ctx, fasthttp.ErrBodyTooLarge)
	if ctx.Response.StatusCode() != 413 || !strings.Contains(string(ctx.Response.Body()), `"mode":"live"`) {
		t.Fatalf("too large = %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx = &fasthttp.RequestCtx{}
	RequestErrorHandler(schemas.ModeLive, MaxBodyBytes)(ctx, fasthttp.ErrNoMultipartForm)
	if ctx.Response.StatusCode() != 400 {
		t.Fatalf("other = %d", ctx.Response.StatusCode())
	}
}
