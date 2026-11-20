package server

// Tenancy area (P3): the hosted server (docs/platform-design.md, Tenancy).
//
// Request path in hosted mode:
//
//	security headers -> host guard -> CORS -> auth middleware (P2, sets the
//	principal) -> tenancy.Dispatcher:
//	  platform route (auth, orgs, members, jobs, runners, tokens, audit,
//	  health)      -> policy check -> platform router, served directly
//	  /api/bench/* -> policy check -> the principal's org runtime router
//	  anything else -> the UI (SPA fallback)
//
// Each org router is built by registerOrgRoutes over that org's Deps only,
// exactly as local mode builds its single router, so a handler reached for
// org A holds no reference to org B's store. Local mode does not use this
// file: server.New is unchanged.

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// RouteRegistrar is a handler area that registers its routes, like every
// handlers.*Handler.
type RouteRegistrar interface {
	RegisterRoutes(r *router.Router, middlewares ...lib.Middleware)
}

// Hosted is what the hosted server is built from. Org-scoped handlers are
// not here: the Registry's builder makes them per org.
type Hosted struct {
	// Auth resolves every request to a principal (P2). Required.
	Auth      lib.Middleware
	Directory tenancy.Directory
	Registry  *tenancy.Registry
	Audit     tenancy.AuditSink
	Policy    *tenancy.Policy // nil: tenancy.DefaultPolicy()
	// DemoSlug is the public read-only org anonymous visitors see.
	DemoSlug string
	// Platform are the areas served directly: auth (P2), orgs and members
	// (P3), jobs (P4), runners (P5), tokens (P2) ...
	Platform []RouteRegistrar
	Ready    []handlers.ReadyCheck
	UI       fs.FS
	Logger   schemas.Logger
	Version  string
	// SweepEvery evicts idle org runtimes; 0 means one minute.
	SweepEvery time.Duration
}

// NewHosted builds the hosted server. cfg.Auth must say hosted.
func NewHosted(cfg *lib.Config, h Hosted) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("new hosted server: config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("new hosted server: %w", err)
	}
	if !cfg.Auth.IsHosted() {
		return nil, errors.New("new hosted server: auth.mode is not hosted")
	}
	if h.Auth == nil || h.Directory == nil || h.Registry == nil {
		return nil, errors.New("new hosted server: auth middleware, org directory and registry are required")
	}
	logger := h.Logger
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	mode := cfg.Server.Mode
	platform := platformRouter(cfg, h.Version, h.Ready, h.Platform)
	var fallback fasthttp.RequestHandler
	if h.UI != nil {
		ui := newRouter(mode)
		NewUIHandler(mode, h.UI).RegisterRoutes(ui)
		fallback = ui.Handler
	}
	disp, err := tenancy.NewDispatcher(tenancy.DispatcherConfig{
		Mode: mode, Policy: h.Policy, Dir: h.Directory, Registry: h.Registry, DemoSlug: h.DemoSlug,
		Platform: platform.Handler, PlatformRoutes: platform.List(), OrgRoutes: OrgRoutes(cfg),
		Fallback: fallback, Audit: h.Audit, Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("new hosted server: %w", err)
	}
	s := &Server{cfg: cfg, logger: logger}
	s.handler = lib.ChainMiddlewares(disp.Handle,
		lib.SecurityHeaders(),
		lib.HostGuard(mode, hostedAllowedHosts(cfg)),
		lib.CORS(cfg.Server.CORSOrigins),
		h.Auth,
	)
	s.server = &fasthttp.Server{
		Handler:               s.handler,
		Name:                  "heimdall",
		MaxRequestBodySize:    lib.MaxBodyBytes,
		ErrorHandler:          lib.RequestErrorHandler(mode, lib.MaxBodyBytes),
		ReadTimeout:           readTimeout,
		IdleTimeout:           idleTimeout,
		NoDefaultServerHeader: true,
		Logger:                fasthttpLogger{logger},
	}
	every := h.SweepEvery
	if every <= 0 {
		every = time.Minute
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				h.Registry.Sweep()
			}
		}
	}()
	// Platform areas with live streams (the jobs handler's log SSE) end them
	// before the HTTP shutdown waits for requests to drain, as local mode does.
	for _, a := range h.Platform {
		if c, ok := a.(interface{ Close() }); ok {
			s.closers = append(s.closers, c.Close)
		}
	}
	var once sync.Once
	s.closers = append(s.closers, func() {
		once.Do(func() {
			close(stop)
			_ = h.Registry.Close()
		})
	})
	return s, nil
}

// hostedAllowedHosts is server.allowed_hosts plus the host of
// auth.public_url, which a hosted server is always reached by.
func hostedAllowedHosts(cfg *lib.Config) []string {
	hosts := append([]string(nil), cfg.Server.AllowedHosts...)
	if u, err := url.Parse(cfg.Auth.PublicURL); err == nil && u.Hostname() != "" {
		hosts = append(hosts, u.Hostname())
	}
	return hosts
}

// platformRouter registers the routes the hosted server serves directly:
// health, readiness and every platform area.
func platformRouter(cfg *lib.Config, version string, ready []handlers.ReadyCheck, areas []RouteRegistrar) *router.Router {
	mode := cfg.Server.Mode
	common := []lib.Middleware{lib.BodyLimit(mode, lib.MaxBodyBytes)}
	r := newRouter(mode)
	handlers.NewHealthHandler(mode, version, nil).RegisterRoutes(r, common...)
	handlers.NewReadinessHandler(mode, version, ready).RegisterRoutes(r, common...)
	for _, area := range areas {
		if area != nil {
			area.RegisterRoutes(r, common...)
		}
	}
	return r
}

// PlatformRoutes lists the platform routes the hosted server registers for
// areas, by method (for the policy coverage test).
func PlatformRoutes(cfg *lib.Config, areas []RouteRegistrar) map[string][]string {
	return platformRouter(cfg, "", nil, areas).List()
}

// newRouter is a router with the bench's JSON 404 and 405 answers.
func newRouter(mode schemas.Mode) *router.Router {
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found: "+string(ctx.Path()))
	}
	r.MethodNotAllowed = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, mode, fasthttp.StatusMethodNotAllowed, lib.ErrTypeMethodNotAllowed,
			"method "+string(ctx.Method())+" not allowed on "+string(ctx.Path()))
	}
	return r
}

// OrgRoutes lists the routes of an org router by method. Registration does
// not depend on the deps, so the list is the same for every org.
func OrgRoutes(cfg *lib.Config) map[string][]string {
	r := router.New()
	registerOrgRoutes(r, cfg, Deps{}, nil, nil)
	return r.List()
}

// OrgRouter is one organization's router.
type OrgRouter struct {
	Handler     fasthttp.RequestHandler
	Routes      map[string][]string
	experiments *handlers.ExperimentsHandler
}

// Close ends the router's live event streams.
func (o *OrgRouter) Close() {
	if o != nil && o.experiments != nil {
		o.experiments.Close()
	}
}

// NewOrgRouter builds the router of orgID over deps, which must be built
// over that org's folders only. cfg is the org's config view (Root is the
// org's project folder). Writes need a principal acting in orgID with at
// least member, and every request must have been resolved to orgID by the
// dispatcher: both are checked again here, behind the policy table.
func NewOrgRouter(cfg *lib.Config, orgID string, deps Deps) *OrgRouter {
	mode := cfg.Server.Mode
	write := tenancy.RequireOrgWriter(mode, orgID)
	common := []lib.Middleware{tenancy.RequireOrgReader(mode, orgID), lib.BodyLimit(mode, lib.MaxBodyBytes)}
	r := newRouter(mode)
	ex := registerOrgRoutes(r, cfg, deps, write, common)
	return &OrgRouter{Handler: r.Handler, Routes: r.List(), experiments: ex}
}
