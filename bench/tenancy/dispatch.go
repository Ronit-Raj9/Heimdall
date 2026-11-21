package tenancy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// APIPrefix is the bench API prefix (handlers.APIPrefix, repeated so this
// package does not import handlers).
const APIPrefix = "/api/bench"

// AuditEntry is one audit_log row (docs/platform-design.md rule 6).
type AuditEntry struct {
	OrgID      string
	ActorType  string
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	IP         string
	At         time.Time
	Details    map[string]any
}

// AuditSink writes audit rows. The database area implements it.
type AuditSink interface {
	Audit(ctx context.Context, e AuditEntry) error
}

// EntryFor fills the actor and network fields of an audit row from the
// request's principal.
func EntryFor(ctx *fasthttp.RequestCtx, orgID, action, targetType, targetID string, details map[string]any) AuditEntry {
	p := auth.PrincipalFrom(ctx)
	actorType := p.ActorType()
	if p.Kind == auth.KindAnonymous {
		actorType = "anonymous"
	}
	return AuditEntry{
		OrgID: orgID, ActorType: actorType, ActorID: p.ActorID(), Action: action,
		TargetType: targetType, TargetID: targetID, IP: ClientIP(ctx), At: time.Now().UTC(), Details: details,
	}
}

// clientIPKey is the request user value holding the client address the
// dispatcher resolved through the trusted proxies.
const clientIPKey = "heimdall.client_ip"

// ClientIP is the request's client address for audit rows: the one the
// dispatcher resolved with DispatcherConfig.ClientIP (the trusted-proxy
// resolver auth routes use), else the transport peer.
func ClientIP(ctx *fasthttp.RequestCtx) string {
	if ip, ok := ctx.UserValue(clientIPKey).(string); ok && ip != "" {
		return ip
	}
	return ctx.RemoteIP().String()
}

// DispatcherConfig is what the hosted server hands the dispatcher.
type DispatcherConfig struct {
	Mode     schemas.Mode
	Policy   *Policy
	Dir      Directory
	Registry *Registry
	// DemoSlug is the public read-only org anonymous callers land in.
	DemoSlug string
	// Platform serves the platform routes; PlatformRoutes lists them
	// (router.List()).
	Platform       fasthttp.RequestHandler
	PlatformRoutes map[string][]string
	// OrgRoutes lists the routes every org router registers. They are the
	// same for every org (registration does not depend on deps), so they are
	// listed once from a template router.
	OrgRoutes map[string][]string
	// Fallback serves everything that is no API route: the UI. Nil answers
	// 404.
	Fallback fasthttp.RequestHandler
	Audit    AuditSink
	Logger   schemas.Logger
	// ClientIP resolves the caller's address through the trusted proxies
	// (auth.Service.ClientIP). Nil uses the transport peer, which behind a
	// reverse proxy is the proxy.
	ClientIP func(*fasthttp.RequestCtx) string
}

// Dispatcher is the hosted root handler behind the auth middleware: it
// matches the route, looks its rule up, resolves the org, authorizes, and
// hands the request to the platform router or to the org's own router.
type Dispatcher struct {
	c        DispatcherConfig
	platform *matcher
	orgs     *matcher
}

// NewDispatcher checks the config and builds the route matchers.
func NewDispatcher(c DispatcherConfig) (*Dispatcher, error) {
	if c.Policy == nil {
		c.Policy = DefaultPolicy()
	}
	if c.Dir == nil || c.Registry == nil || c.Platform == nil {
		return nil, errors.New("hosted dispatcher: directory, registry and platform router are required")
	}
	if c.Logger == nil {
		c.Logger = schemas.NoopLogger{}
	}
	pm, err := newMatcher(c.PlatformRoutes)
	if err != nil {
		return nil, fmt.Errorf("hosted dispatcher: platform routes: %w", err)
	}
	om, err := newMatcher(c.OrgRoutes)
	if err != nil {
		return nil, fmt.Errorf("hosted dispatcher: org routes: %w", err)
	}
	return &Dispatcher{c: c, platform: pm, orgs: om}, nil
}

// Handle serves one request. The auth middleware must have run.
func (d *Dispatcher) Handle(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	path := string(ctx.Request.URI().PathOriginal())
	p := auth.PrincipalFrom(ctx)
	if d.c.ClientIP != nil {
		ctx.SetUserValue(clientIPKey, d.c.ClientIP(ctx))
	}

	if pat, ok := d.platform.match(ctx, method, path); ok {
		rule, _ := d.c.Policy.PlatformRule(method, pat)
		o, eff, ok := d.authorize(ctx, rule, method, p, userString(ctx, OrgPathParam))
		if !ok {
			return
		}
		if o != nil {
			SetOrg(ctx, o)
		}
		auth.SetPrincipal(ctx, eff)
		d.c.Platform(ctx)
		return
	}
	if path != APIPrefix && !strings.HasPrefix(path, APIPrefix+"/") {
		if d.c.Fallback != nil {
			d.c.Fallback(ctx)
			return
		}
		lib.SendError(ctx, d.c.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found: "+path)
		return
	}
	pat, ok := d.orgs.match(ctx, method, path)
	if !ok {
		if d.orgs.otherMethod(ctx, method, path) || d.platform.otherMethod(ctx, method, path) {
			lib.SendError(ctx, d.c.Mode, fasthttp.StatusMethodNotAllowed, lib.ErrTypeMethodNotAllowed, "method "+method+" not allowed on "+path)
			return
		}
		lib.SendError(ctx, d.c.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found: "+path)
		return
	}
	rule, _ := d.c.Policy.OrgRule(method, pat)
	o, eff, ok := d.authorize(ctx, rule, method, p, "")
	if !ok {
		return
	}
	if o == nil {
		// Every org route has an org; a rule without one is a table error.
		lib.SendError(ctx, d.c.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "route has no organization")
		return
	}
	actx, cancel := workContext()
	defer cancel()
	rt, release, err := d.c.Registry.Acquire(actx, *o)
	if err != nil {
		d.c.Logger.Error("org runtime unavailable", "org", o.ID, "error", err.Error())
		lib.SendError(ctx, d.c.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "organization runtime unavailable")
		return
	}
	defer release()
	SetOrg(ctx, o)
	auth.SetPrincipal(ctx, eff)
	// A stream opened by this request re-authorizes the original caller
	// while it lasts (membership removed, org made private): the check here
	// ran once, at the start.
	headerRef := string(ctx.Request.Header.Peek(OrgHeader))
	lib.SetStreamGuard(ctx, func() bool {
		gctx, cancel := workContext()
		defer cancel()
		ro, reff, err := Resolve(gctx, d.c.Dir, rule, p, "", headerRef, d.c.DemoSlug)
		if err == nil {
			err = Authorize(rule, method, reff, ro)
		}
		return err == nil && ro != nil && ro.ID == o.ID
	})
	rt.Handler(ctx)
	if !IsRead(method) && !rule.NoAudit && ctx.Response.StatusCode() < 400 {
		d.audit(ctx, o.ID, method, pat)
	}
}

// workContext bounds the dispatcher's database and build work. It never
// derives from *fasthttp.RequestCtx, whose Done reads server state without a
// lock (handlers/common.go has the same rule).
func workContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// authorize resolves the org and checks the rule, answering the request
// itself when it is refused.
func (d *Dispatcher) authorize(ctx *fasthttp.RequestCtx, rule Rule, method string, p *auth.Principal, pathRef string) (*Org, *auth.Principal, bool) {
	rctx, cancel := workContext()
	defer cancel()
	o, eff, err := Resolve(rctx, d.c.Dir, rule, p, pathRef, string(ctx.Request.Header.Peek(OrgHeader)), d.c.DemoSlug)
	if err == nil {
		err = Authorize(rule, method, eff, o)
	}
	if err == nil {
		return o, eff, true
	}
	var den *Denied
	if errors.As(err, &den) {
		if den.Status == fasthttp.StatusUnauthorized {
			ctx.Response.Header.Set(fasthttp.HeaderWWWAuthenticate, `Bearer realm="heimdall"`)
		}
		lib.SendError(ctx, d.c.Mode, den.Status, den.Type, den.Msg)
		return nil, nil, false
	}
	d.c.Logger.Error("resolve organization", "error", err.Error())
	lib.SendError(ctx, d.c.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "resolve organization failed")
	return nil, nil, false
}

// createdKey is the request user value naming the record a create route
// committed (SetCreated).
const createdKey = "heimdall.created_id"

// SetCreated records the ID of the resource a create handler committed, so
// the generic audit row of the write names it. Collection-create routes have
// no path parameter to take it from.
func SetCreated(ctx *fasthttp.RequestCtx, id string) { ctx.SetUserValue(createdKey, id) }

// audit writes the generic row of an org-router write. Platform handlers
// write their own, more specific rows.
func (d *Dispatcher) audit(ctx *fasthttp.RequestCtx, orgID, method, pattern string) {
	if d.c.Audit == nil {
		return
	}
	action, targetType, targetID := ActionOf(ctx, method, pattern)
	details := map[string]any{"route": method + " " + pattern, "status": ctx.Response.StatusCode()}
	if id, _ := ctx.UserValue(createdKey).(string); id != "" {
		if targetID == "" {
			targetID = id
		} else {
			details["created_id"] = id
		}
	}
	e := EntryFor(ctx, orgID, action, targetType, targetID, details)
	actx, cancel := workContext()
	defer cancel()
	if err := d.c.Audit.Audit(actx, e); err != nil {
		d.c.Logger.Error("audit write failed", "org", orgID, "action", action, "error", err.Error())
	}
}

// ActionOf names an API write as a dotted verb from its pattern:
// POST /api/bench/experiments/{id}/start is experiments.start on
// experiments/<id>; POST /api/bench/experiments is experiments.create.
func ActionOf(ctx *fasthttp.RequestCtx, method, pattern string) (action, targetType, targetID string) {
	rest := strings.TrimPrefix(pattern, APIPrefix+"/")
	var words []string
	for _, seg := range strings.Split(rest, "/") {
		if strings.HasPrefix(seg, "{") {
			name := strings.Trim(seg, "{}")
			if targetID == "" {
				targetID = userString(ctx, name)
				targetType = strings.Join(words, ".")
			}
			continue
		}
		if seg != "" {
			words = append(words, seg)
		}
	}
	if targetType == "" && len(words) > 0 {
		targetType = words[0]
	}
	action = strings.Join(words, ".")
	if len(words) == 1 || (targetID != "" && strings.HasSuffix(pattern, "}")) {
		action += "." + strings.ToLower(map[string]string{"POST": "create", "PUT": "update", "PATCH": "update", "DELETE": "delete"}[method])
	}
	return strings.TrimSuffix(action, "."), targetType, targetID
}

func userString(ctx *fasthttp.RequestCtx, name string) string {
	switch v := ctx.UserValue(name).(type) {
	case string:
		return v
	case []byte:
		return string(v)
	}
	return ""
}

// matcher finds the router pattern a request matches without running any
// handler: a router whose every route only records its own pattern.
type matcher struct {
	r       *router.Router
	methods []string
}

const patternKey = "heimdall.tenancy.pattern"

func newMatcher(routes map[string][]string) (m *matcher, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("register route: %v", p)
		}
	}()
	r := router.New()
	var methods []string
	for method, pats := range routes {
		methods = append(methods, method)
		for _, pat := range pats {
			pattern := pat
			r.Handle(method, pattern, func(ctx *fasthttp.RequestCtx) { ctx.SetUserValue(patternKey, pattern) })
		}
	}
	sort.Strings(methods)
	return &matcher{r: r, methods: methods}, nil
}

// match returns the pattern method and path match, setting the path
// parameters as user values like the real router does.
func (m *matcher) match(ctx *fasthttp.RequestCtx, method, path string) (string, bool) {
	h, _ := m.r.Lookup(method, path, ctx)
	if h == nil {
		return "", false
	}
	ctx.SetUserValue(patternKey, nil)
	h(ctx)
	pat, ok := ctx.UserValue(patternKey).(string)
	return pat, ok && pat != ""
}

// otherMethod reports whether path matches a route under another method.
func (m *matcher) otherMethod(ctx *fasthttp.RequestCtx, method, path string) bool {
	for _, other := range m.methods {
		if other == method {
			continue
		}
		if h, _ := m.r.Lookup(other, path, ctx); h != nil {
			return true
		}
	}
	return false
}

// RequireOrgWriter is the write middleware every hosted org router's
// handlers get, as defence in depth behind the dispatcher: the request's
// principal must act in orgID with at least member.
func RequireOrgWriter(mode schemas.Mode, orgID string) lib.Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			p := auth.PrincipalFrom(ctx)
			if p.OrgID != orgID || !(p.Can(auth.RoleMember) || p.Kind == auth.KindRunner) {
				lib.SendError(ctx, mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "write not allowed in this organization")
				return
			}
			next(ctx)
		}
	}
}

// RequireOrgReader is the read middleware of a hosted org router: the
// request must have been resolved to orgID by the dispatcher. A request
// that reaches the router any other way is refused.
func RequireOrgReader(mode schemas.Mode, orgID string) lib.Middleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			o, ok := OrgFrom(ctx)
			if !ok || o.ID != orgID || auth.PrincipalFrom(ctx).OrgID != orgID {
				lib.SendError(ctx, mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "request not resolved to this organization")
				return
			}
			next(ctx)
		}
	}
}
