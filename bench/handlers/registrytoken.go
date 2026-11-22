package handlers

// Library registry pull tokens (INFRA_I4 item 4, bench/imagepin): the token
// endpoint of Docker registry token authentication. The library registry
// (deploy/hosted/registry/config-library.yml) sends every docker client
// here; the client presents a Heimdall credential as its password (`docker
// login <host> -u <anything>` with an org API token hm_... or a runner
// token hmr_..., or the operator's sync identity) and gets a short-lived
// token for exactly what it may do: an org pulls the open library and the
// licence-restricted suites the operator's access file allows it, only the
// sync identity pushes. GET is the classic flow (basic auth), POST the
// OAuth2 password grant containerd uses.

import (
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/imagepin"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
)

// RegistryTokenPath is the token endpoint (the registry's auth.token.realm).
const RegistryTokenPath = APIPrefix + "/registry/token"

// RegistryTokenResponse is the token endpoint's answer. Token and
// AccessToken carry the same JWT (both names are in use by clients).
type RegistryTokenResponse struct {
	Token       string `json:"token,omitempty"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	IssuedAt    string `json:"issued_at"`
	Scope       string `json:"scope,omitempty"`
}

// RegistryOrgResolver maps a credential to the org it belongs to; an error
// is a refused credential.
type RegistryOrgResolver func(ctx context.Context, secret string) (orgID string, err error)

// RegistryTokenHandler issues library registry tokens.
type RegistryTokenHandler struct {
	mode     schemas.Mode
	issuer   *imagepin.Issuer
	policy   *imagepin.AccessPolicy
	resolve  RegistryOrgResolver
	syncUser string
	syncHash []byte // bcrypt of the sync password; nil: nobody pushes
	off      string // why the endpoint is off; "" when configured

	// FailuresPerMinute caps refused credentials per client address.
	FailuresPerMinute int
	// ClientIP resolves the caller through the trusted proxies (hosted:
	// auth.Service.ClientIP); nil is the transport peer.
	ClientIP func(*fasthttp.RequestCtx) string
	now      func() time.Time
	mu       sync.Mutex
	fails    map[string][]time.Time
}

// NewRegistryTokenHandler builds the handler. A nil issuer answers 503 with
// off. syncLine is an htpasswd line "user:<bcrypt>" for the sync identity
// (the `registry htpasswd` tool writes one), empty for none.
func NewRegistryTokenHandler(mode schemas.Mode, issuer *imagepin.Issuer, policy *imagepin.AccessPolicy, resolve RegistryOrgResolver, syncLine, off string) *RegistryTokenHandler {
	h := &RegistryTokenHandler{mode: mode, issuer: issuer, policy: policy, resolve: resolve, off: off,
		FailuresPerMinute: 20, now: time.Now, fails: map[string][]time.Time{}}
	if u, hash, ok := strings.Cut(strings.TrimSpace(syncLine), ":"); ok && u != "" && strings.HasPrefix(hash, "$2") {
		h.syncUser, h.syncHash = u, []byte(hash)
	}
	if h.policy == nil {
		h.policy, _ = imagepin.LoadAccessPolicy("")
	}
	return h
}

// RegisterRoutes registers GET and POST /registry/token.
func (h *RegistryTokenHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(RegistryTokenPath, lib.ChainMiddlewares(h.get, middlewares...))
	r.POST(RegistryTokenPath, lib.ChainMiddlewares(h.post, middlewares...))
}

func (h *RegistryTokenHandler) get(ctx *fasthttp.RequestCtx) {
	user, pass, ok := basicAuth(ctx)
	if !ok {
		h.challenge(ctx)
		return
	}
	args := ctx.QueryArgs()
	var scopes []string
	for _, v := range args.PeekMulti("scope") {
		scopes = append(scopes, string(v))
	}
	h.issue(ctx, string(args.Peek("service")), user, pass, scopes, false)
}

func (h *RegistryTokenHandler) post(ctx *fasthttp.RequestCtx) {
	form := ctx.PostArgs()
	if string(form.Peek("grant_type")) != "password" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "unsupported grant_type: only password is accepted (username and password, the Heimdall credential as the password)")
		return
	}
	h.issue(ctx, string(form.Peek("service")), string(form.Peek("username")), string(form.Peek("password")),
		[]string{string(form.Peek("scope"))}, true)
}

func (h *RegistryTokenHandler) issue(ctx *fasthttp.RequestCtx, service, user, pass string, scopes []string, oauth bool) {
	if h.issuer == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "the library registry is not configured on this server: "+h.off)
		return
	}
	if service != "" && service != h.issuer.Service {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "unknown registry service "+service+"; this server issues tokens for "+h.issuer.Service)
		return
	}
	ip := registryClientIP(ctx)
	if h.ClientIP != nil {
		ip = h.ClientIP(ctx)
	}
	if h.limited(ip) {
		ctx.Response.Header.Set("Retry-After", "60")
		lib.SendError(ctx, h.mode, fasthttp.StatusTooManyRequests, lib.ErrTypeUnavailable, "too many refused registry credentials from this address; retry in a minute")
		return
	}
	id, subject, ok := h.identify(ctx, user, pass)
	if !ok {
		h.failed(ip)
		h.challenge(ctx)
		return
	}
	granted := h.policy.Grant(id, imagepin.ParseScopes(scopes))
	tok, ttl, at, err := h.issuer.Issue(subject, granted)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "could not sign the registry token")
		return
	}
	out := RegistryTokenResponse{AccessToken: tok, ExpiresIn: ttl, IssuedAt: at.UTC().Format(time.RFC3339)}
	if !oauth {
		out.Token = tok
	}
	var sc []string
	for _, a := range granted {
		sc = append(sc, a.Type+":"+a.Name+":"+strings.Join(a.Actions, ","))
	}
	out.Scope = strings.Join(sc, " ")
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// identify checks the credential: the sync identity by its bcrypt hash,
// else an org credential through the resolver.
func (h *RegistryTokenHandler) identify(ctx *fasthttp.RequestCtx, user, pass string) (imagepin.Identity, string, bool) {
	if pass == "" {
		return imagepin.Identity{}, "", false
	}
	if h.syncHash != nil && user == h.syncUser {
		if bcrypt.CompareHashAndPassword(h.syncHash, []byte(pass)) != nil {
			return imagepin.Identity{}, "", false
		}
		return imagepin.Identity{Sync: true}, "sync:" + user, true
	}
	if h.resolve == nil {
		return imagepin.Identity{}, "", false
	}
	org, err := h.resolve(ctx, pass)
	if err != nil || org == "" {
		return imagepin.Identity{}, "", false
	}
	return imagepin.Identity{OrgID: org}, "org:" + org, true
}

func (h *RegistryTokenHandler) challenge(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("WWW-Authenticate", `Basic realm="heimdall library registry"`)
	lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized,
		"the library registry needs a Heimdall credential as the password: an org API token (hm_...) or a runner token (hmr_...), e.g. `bifrost-bench images login`")
}

func (h *RegistryTokenHandler) limited(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	cut := h.now().Add(-time.Minute)
	keep := h.fails[ip][:0]
	for _, t := range h.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(h.fails, ip)
	} else {
		h.fails[ip] = keep
	}
	return h.FailuresPerMinute > 0 && len(keep) >= h.FailuresPerMinute
}

func (h *RegistryTokenHandler) failed(ip string) {
	h.mu.Lock()
	h.fails[ip] = append(h.fails[ip], h.now())
	h.mu.Unlock()
}

func basicAuth(ctx *fasthttp.RequestCtx) (user, pass string, ok bool) {
	h := strings.TrimSpace(string(ctx.Request.Header.Peek(fasthttp.HeaderAuthorization)))
	if len(h) < 6 || !strings.EqualFold(h[:6], "basic ") {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[6:]))
	if err != nil {
		return "", "", false
	}
	user, pass, ok = strings.Cut(string(raw), ":")
	return user, pass, ok
}

func registryClientIP(ctx *fasthttp.RequestCtx) string {
	if ip := ctx.RemoteIP(); ip != nil {
		return ip.String()
	}
	host, _, _ := net.SplitHostPort(ctx.RemoteAddr().String())
	return host
}

func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "GET", Path: RegistryTokenPath, Tag: "registry", Auth: AuthNone,
			Summary:  "Library registry pull token (Docker registry token auth): basic auth with an org API or runner token as the password; grants pull on the open library and on the restricted suites the org may use; 503 while the registry is not configured",
			Response: RegistryTokenResponse{}},
		RouteDoc{Method: "POST", Path: RegistryTokenPath, Tag: "registry", Auth: AuthNone,
			Summary:  "Library registry token, OAuth2 password grant (form: grant_type=password, username, password, scope, service), as containerd asks",
			Response: RegistryTokenResponse{}},
	)
}
