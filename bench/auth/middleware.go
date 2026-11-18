package auth

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Names on the wire.
const (
	// SessionCookie holds the session id (32 random bytes, base64url).
	SessionCookie = "heimdall_session"
	// OAuthStateCookie binds a GitHub sign-in to the browser that started it.
	OAuthStateCookie = "heimdall_oauth_state"
	// CSRFHeader carries the session's CSRF value on cookie-authenticated
	// writes.
	CSRFHeader = "X-Heimdall-CSRF"
)

// Middleware resolves every request to a Principal before any handler runs.
//
// Local mode (cfg.Hosted false) keeps today's behaviour exactly: nothing is
// checked here, reads stay open and the routes that wrap
// lib.RequireWriteToken keep requiring the write token; every request gets
// the local principal (org "local", role owner). svc may be nil.
//
// Hosted mode:
//   - "Authorization: Bearer hm_..." is an API token, "hmr_..." a runner
//     token (handed to the registered RunnerVerifier). A bad one is 401;
//     there is no fallback to the cookie. Bearer requests need no CSRF
//     header: browsers never attach that header cross-site on their own.
//   - Otherwise the heimdall_session cookie, when it names a live session,
//     makes a user principal. A write (any method but GET, HEAD, OPTIONS)
//     on a cookie also needs X-Heimdall-CSRF equal to the session's value
//     and an Origin (or, without one, a Referer) among cfg.AllowedOrigins,
//     or Sec-Fetch-Site: same-origin when the browser sent "Origin: null"
//     (see originVerdict).
//   - Otherwise the request is anonymous. An anonymous write that shows a
//     foreign origin is refused (login CSRF).
func Middleware(cfg Config, svc *Service) lib.Middleware {
	mode := cfg.ServerMode
	if !cfg.Hosted {
		return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
			return func(ctx *fasthttp.RequestCtx) {
				SetPrincipal(ctx, Local())
				next(ctx)
			}
		}
	}
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			if svc == nil {
				lib.SendError(ctx, mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "auth service is not configured")
				return
			}
			p, err := svc.Authenticate(ctx)
			if err != nil {
				svc.Fail(ctx, err)
				return
			}
			SetPrincipal(ctx, p)
			next(ctx)
		}
	}
}

// Authenticate resolves the request's credential to a principal and runs
// the CSRF and Origin checks; it is the body of Middleware in hosted mode.
func (s *Service) Authenticate(ctx *fasthttp.RequestCtx) (*Principal, error) {
	dctx, cancel := dbCtx()
	defer cancel()
	write := !isSafeMethod(ctx)
	if raw, ok := bearer(ctx); ok {
		var (
			p   *Principal
			err error
		)
		switch {
		case strings.HasPrefix(raw, RunnerTokenPrefix):
			p, err = verifyRunner(dctx, raw)
		case strings.HasPrefix(raw, APITokenPrefix):
			p, err = s.tokenPrincipal(dctx, raw)
		default:
			err = ErrInvalidToken
		}
		if err != nil {
			return nil, err
		}
		return p, nil
	}
	if cookie := ctx.Request.Header.Cookie(SessionCookie); len(cookie) > 0 {
		p, err := s.sessionPrincipal(dctx, string(cookie))
		if err != nil {
			return nil, err
		}
		if p != nil {
			if write {
				if err := s.checkCSRF(ctx, p); err != nil {
					return nil, err
				}
			}
			return p, nil
		}
		// A stale cookie: forget it and carry on anonymous.
		s.clearCookie(ctx, SessionCookie, "/")
	}
	if write && s.originVerdict(ctx) == originForeign {
		return nil, ErrOrigin
	}
	return Anonymous(), nil
}

// checkCSRF insists on the session's CSRF header and an allowed origin.
func (s *Service) checkCSRF(ctx *fasthttp.RequestCtx, p *Principal) error {
	got := ctx.Request.Header.Peek(CSRFHeader)
	if len(got) == 0 || p.csrf == "" || subtle.ConstantTimeCompare(got, []byte(p.csrf)) != 1 {
		return ErrCSRF
	}
	if s.originVerdict(ctx) != originAllowed {
		return ErrCSRF
	}
	return nil
}

// Origin verdicts.
const (
	originUnknown = iota // the request carries no browser origin signal
	originAllowed
	originForeign
)

// originVerdict decides where a write comes from, using in turn:
//  1. the Origin header, unless it is absent or "null";
//  2. the origin of the Referer header;
//  3. Sec-Fetch-Site, which browsers set and pages can not: only
//     "same-origin" is allowed.
//
// The UI is served with Referrer-Policy: no-referrer, under which browsers
// send "Origin: null" and no Referer even on same-origin writes; step 3 is
// what recognises those. An Origin of "null" with no Sec-Fetch-Site is
// foreign. No signal at all is unknown (a non-browser client).
func (s *Service) originVerdict(ctx *fasthttp.RequestCtx) int {
	h := &ctx.Request.Header
	allowed := func(o string) int {
		if o != "" && slices.Contains(s.cfg.AllowedOrigins, o) {
			return originAllowed
		}
		return originForeign
	}
	origin := strings.TrimSpace(string(h.Peek(fasthttp.HeaderOrigin)))
	if origin != "" && origin != "null" {
		return allowed(originOf(origin))
	}
	if ref := strings.TrimSpace(string(h.Peek(fasthttp.HeaderReferer))); ref != "" {
		return allowed(originOf(ref))
	}
	if site := strings.TrimSpace(string(h.Peek("Sec-Fetch-Site"))); site != "" {
		if strings.EqualFold(site, "same-origin") {
			return originAllowed
		}
		return originForeign
	}
	if origin == "null" {
		return originForeign
	}
	return originUnknown
}

func isSafeMethod(ctx *fasthttp.RequestCtx) bool {
	return ctx.IsGet() || ctx.IsHead() || ctx.IsOptions()
}

// bearer returns the token of "Authorization: Bearer <token>".
func bearer(ctx *fasthttp.RequestCtx) (string, bool) {
	h := ctx.Request.Header.Peek(fasthttp.HeaderAuthorization)
	if len(h) < 7 || !bytes.EqualFold(h[:7], []byte("Bearer ")) {
		return "", false
	}
	tok := strings.TrimSpace(string(h[7:]))
	return tok, tok != ""
}

// sendAuthError answers err with its status; internal errors stay vague.
func sendAuthError(ctx *fasthttp.RequestCtx, mode schemas.Mode, err error) {
	if e, ok := AsError(err); ok {
		if e.Status == fasthttp.StatusUnauthorized {
			ctx.Response.Header.Set(fasthttp.HeaderWWWAuthenticate, `Bearer realm="heimdall"`)
		}
		if e.RetryAfter > 0 {
			ctx.Response.Header.Set(fasthttp.HeaderRetryAfter, strconv.Itoa(int((e.RetryAfter+time.Second-1)/time.Second)))
		}
		lib.SendError(ctx, mode, e.Status, e.Type, e.Msg)
		return
	}
	if err == context.DeadlineExceeded {
		lib.SendError(ctx, mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "auth: database timed out")
		return
	}
	lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "auth: internal error")
}

// SendError answers an error returned by the Service the way the
// middleware does: its own status for *Error, 500 otherwise.
func SendError(ctx *fasthttp.RequestCtx, mode schemas.Mode, err error) { sendAuthError(ctx, mode, err) }

// Fail answers err like SendError and logs it when it is not a client
// error, so a database failure is visible to the operator while the caller
// only sees "internal error".
func (s *Service) Fail(ctx *fasthttp.RequestCtx, err error) {
	if e, ok := AsError(err); !ok || e.Status >= 500 {
		s.logger.Error(fmt.Sprintf("auth: %s %s: %v", ctx.Method(), ctx.Path(), err))
	}
	sendAuthError(ctx, s.cfg.ServerMode, err)
}

// ---- cookies ----

// SetSessionCookie writes the session cookie: HttpOnly, SameSite=Lax,
// Path=/, Secure when the public URL is https, lasting as long as the
// session's absolute lifetime.
func (s *Service) SetSessionCookie(ctx *fasthttp.RequestCtx, sess *NewSession) {
	c := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(c)
	c.SetKey(SessionCookie)
	c.SetValue(sess.Cookie)
	c.SetPath("/")
	c.SetHTTPOnly(true)
	c.SetSecure(s.cfg.SecureCookies())
	c.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	if d := time.Until(sess.Expires); d > 0 {
		c.SetMaxAge(int(d / time.Second))
	}
	ctx.Response.Header.SetCookie(c)
}

// ClearSessionCookie expires the session cookie in the browser.
func (s *Service) ClearSessionCookie(ctx *fasthttp.RequestCtx) {
	s.clearCookie(ctx, SessionCookie, "/")
}

func (s *Service) clearCookie(ctx *fasthttp.RequestCtx, name, path string) {
	c := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(c)
	c.SetKey(name)
	c.SetValue("")
	c.SetPath(path)
	c.SetHTTPOnly(true)
	c.SetSecure(s.cfg.SecureCookies())
	c.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	c.SetExpire(fasthttp.CookieExpireDelete)
	c.SetMaxAge(-1)
	ctx.Response.Header.SetCookie(c)
}

// ---- client address ----

// ClientIP is the caller's address for rate limits and audit rows: the TCP
// peer, or, when the peer is a trusted proxy, the right-most address in
// X-Forwarded-For that is not itself a trusted proxy.
func (s *Service) ClientIP(ctx *fasthttp.RequestCtx) string {
	return clientIP(ctx, s.cfg.TrustedProxies)
}

func clientIP(ctx *fasthttp.RequestCtx, trusted []*net.IPNet) string {
	peer := ctx.RemoteIP()
	if !inNets(peer, trusted) {
		return peer.String()
	}
	xff := string(ctx.Request.Header.Peek(fasthttp.HeaderXForwardedFor))
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil {
			break
		}
		if !inNets(ip, trusted) {
			return ip.String()
		}
	}
	return peer.String()
}

func inNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// safeReturnTo accepts only a same-site absolute path: "/..." without a
// scheme, host, backslash or control character, at most 512 bytes. Anything
// else becomes "/".
func safeReturnTo(raw string) string {
	if raw == "" || len(raw) > 512 || raw[0] != '/' || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\") {
		return "/"
	}
	for _, c := range raw {
		if c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	return raw
}

// RequireRole answers 401 to an unauthenticated principal and 403 to one
// below min. It reads the principal Middleware set.
func RequireRole(mode schemas.Mode, min Role) lib.Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			p := PrincipalFrom(ctx)
			if !p.IsAuthenticated() {
				sendAuthError(ctx, mode, ErrUnauthenticated)
				return
			}
			if !p.Can(min) {
				sendAuthError(ctx, mode, errForbidden("this action needs role %s", min))
				return
			}
			next(ctx)
		}
	}
}

// WriteGuard is the per-route guard handlers take as their write
// middleware. Local mode: exactly lib.RequireWriteToken with the write token,
// as today. Hosted mode: a member or above of the request's org (the
// tenancy dispatcher's policy table is the finer check).
func WriteGuard(cfg Config) lib.Middleware {
	if !cfg.Hosted {
		return lib.RequireWriteToken(cfg.ServerMode, cfg.WriteToken)
	}
	return RequireRole(cfg.ServerMode, RoleMember)
}
