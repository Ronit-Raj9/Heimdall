package lib

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// MaxBodyBytes caps every request body. The largest legitimate body is an
// experiment YAML or a pasted audit table, both far below 1 MB.
const MaxBodyBytes = 1 << 20

// MaxUploadBytes caps upload routes (OrgEnv.U) and the server body size.
const MaxUploadBytes = 64 << 20

// WriteTokenEnv names the environment variable holding the write token.
const WriteTokenEnv = "BENCH_WRITE_TOKEN"

// ContentSecurityPolicy is the policy for every response. Trace content is
// rendered as inert text, so scripts only ever come from the bundle itself.
// Inline styles stay allowed because the UI's component library injects them.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// Middleware wraps a handler, like Bifrost's BifrostHTTPMiddleware.
type Middleware func(next fasthttp.RequestHandler) fasthttp.RequestHandler

// ChainMiddlewares applies middlewares so the first one runs first. Any of
// them may short-circuit by not calling next.
func ChainMiddlewares(handler fasthttp.RequestHandler, middlewares ...Middleware) fasthttp.RequestHandler {
	chained := handler
	for i := len(middlewares) - 1; i >= 0; i-- {
		if middlewares[i] != nil {
			chained = middlewares[i](chained)
		}
	}
	return chained
}

// ResolveWriteToken returns the token from BENCH_WRITE_TOKEN, or a fresh
// random one. generated is true when the caller must show the token once.
func ResolveWriteToken() (token string, generated bool, err error) {
	if v := strings.TrimSpace(os.Getenv(WriteTokenEnv)); v != "" {
		return v, false, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("generate write token: %w", err)
	}
	return hex.EncodeToString(buf), true, nil
}

// RequireWriteToken rejects a request without "Authorization: Bearer <token>".
// It guards every POST route; loopback binding is the first defence and this
// is the second, against other local users and browser pages.
func RequireWriteToken(mode schemas.Mode, token string) Middleware {
	want := []byte(token)
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			got, ok := bytes.CutPrefix(ctx.Request.Header.Peek(fasthttp.HeaderAuthorization), []byte("Bearer "))
			if len(want) == 0 || !ok || subtle.ConstantTimeCompare(bytes.TrimSpace(got), want) != 1 {
				ctx.Response.Header.Set(fasthttp.HeaderWWWAuthenticate, `Bearer realm="bifrost-bench"`)
				SendError(ctx, mode, fasthttp.StatusUnauthorized, ErrTypeUnauthorized, "write token required: Authorization: Bearer <token>")
				return
			}
			next(ctx)
		}
	}
}

// BodyLimit rejects bodies over limit bytes with 413. The server also sets
// fasthttp's MaxRequestBodySize; this check keeps handlers safe when they are
// mounted on a server without it.
func BodyLimit(mode schemas.Mode, limit int) Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			if ctx.Request.Header.ContentLength() > limit || len(ctx.Request.Body()) > limit {
				SendError(ctx, mode, fasthttp.StatusRequestEntityTooLarge, ErrTypeTooLarge,
					fmt.Sprintf("request body exceeds the limit of %d bytes", limit))
				return
			}
			next(ctx)
		}
	}
}

// SecurityHeaders sets the CSP and the usual hardening headers on every
// response, API and UI alike. A handler that asked for a sandboxed response
// (a CSP of exactly "sandbox", as the artifact download does for
// agent-produced files) keeps the sandbox on top of the global policy.
func SecurityHeaders() Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			next(ctx)
			h := &ctx.Response.Header
			csp := ContentSecurityPolicy
			if string(h.Peek("Content-Security-Policy")) == "sandbox" {
				csp += "; sandbox"
			}
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
		}
	}
}

// HostGuard rejects requests whose Host header is not a loopback name or one
// of allowed. A loopback-bound server is still reachable from a browser
// through DNS rebinding; checking Host closes that path for the read routes,
// which the write token does not cover.
func HostGuard(mode schemas.Mode, allowed []string) Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			host := string(ctx.Host())
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if !IsLoopbackHost(host) && !slices.ContainsFunc(allowed, func(a string) bool { return strings.EqualFold(a, host) }) {
				SendError(ctx, mode, fasthttp.StatusForbidden, ErrTypeForbidden, "host not allowed: "+host)
				return
			}
			next(ctx)
		}
	}
}

// CORS answers cross-origin requests only from the listed origins. With no
// origins it adds nothing, so browsers keep the same-origin default.
func CORS(origins []string) Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		if len(origins) == 0 {
			return next
		}
		return func(ctx *fasthttp.RequestCtx) {
			origin := string(ctx.Request.Header.Peek(fasthttp.HeaderOrigin))
			if origin != "" && slices.Contains(origins, origin) {
				h := &ctx.Response.Header
				h.Set(fasthttp.HeaderAccessControlAllowOrigin, origin)
				// The hosted browser client always sends its session cookie
				// (credentials: "include"); without this header browsers
				// drop the response for an explicitly allowed origin. Only
				// listed origins get it, never a wildcard.
				h.Set(fasthttp.HeaderAccessControlAllowCredentials, "true")
				h.Set(fasthttp.HeaderAccessControlAllowMethods, "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set(fasthttp.HeaderAccessControlAllowHeaders, "Authorization, Content-Type, X-Heimdall-CSRF, X-Heimdall-Org") // Auth area (P2): CSRF and org headers of hosted mode
				h.Set(fasthttp.HeaderVary, "Origin")
				if ctx.IsOptions() {
					ctx.SetStatusCode(fasthttp.StatusNoContent)
					return
				}
			}
			next(ctx)
		}
	}
}

// RequestErrorHandler is the fasthttp server's ErrorHandler. It answers an
// oversized body with the bench's 413 JSON error instead of fasthttp's
// generic 400.
func RequestErrorHandler(mode schemas.Mode, limit int) func(*fasthttp.RequestCtx, error) {
	return func(ctx *fasthttp.RequestCtx, err error) {
		if errors.Is(err, fasthttp.ErrBodyTooLarge) {
			ctx.Response.Reset()
			SendError(ctx, mode, fasthttp.StatusRequestEntityTooLarge, ErrTypeTooLarge,
				fmt.Sprintf("request body exceeds the limit of %d bytes", limit))
			return
		}
		SendError(ctx, mode, fasthttp.StatusBadRequest, ErrTypeBadRequest, "malformed request")
	}
}
