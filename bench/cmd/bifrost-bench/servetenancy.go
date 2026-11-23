package main

// Tenancy area (P3): serve in hosted mode (auth.mode: hosted). Every
// organization is served from its own folders (tenancy.Layout:
// runs/orgs/<org_id>/ for evidence, runs/projects/<org_id>/ for suites,
// agents, experiments, policies and releases) by a router built with the
// same orgDeps local mode uses, through the tenancy registry. Platform
// routes (auth, orgs, members, jobs, runners, tokens) are served directly.
//
// Other areas plug in here without editing this file:
//   - hostedAreas: platform route areas (auth, jobs, runners ...), appended
//     from an init() in the area's own file;
//   - orgLauncher: the launcher and worker API of one hosted org (jobs and
//     runners areas). Unset, the org's experiment write routes answer 503
//     and nothing runs on the hosted server.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/maximhq/bifrost/bench/mail"
	"os"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/release"
	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// hostedEnv is what hosted-mode wiring shares: the base app (instance
// config, logger, registry), the database, the auth service and the
// tenancy registry.
type hostedEnv struct {
	ctx      context.Context
	base     *app
	db       *db.DB
	authCfg  auth.Config
	auth     *auth.Service
	authMW   lib.Middleware // nil: auth.Middleware(authCfg, auth)
	layout   tenancy.Layout
	registry *tenancy.Registry
	dir      tenancy.Directory
	audit    tenancy.AuditSink
	offline  bool
	// platformRoutes are the platform router's routes, set when the server
	// is built (newHostedServer), before any org runtime is.
	platformRoutes map[string][]string
	// server is the hosted server once newHostedServer built it; the remote
	// MCP endpoint replays tool calls through its handler (mcphttp_hosted.go).
	server *server.Server
}

// hostedAreas build the platform route areas of hosted mode. Each area
// appends its constructor from an init() in its own file.
var hostedAreas []func(env *hostedEnv) (server.RouteRegistrar, error)

// orgLauncher builds the launcher and the worker API of one hosted org over
// that org's app oa and index ix. The jobs and runners areas set it; nil
// leaves the org without a launcher (experiment writes answer 503).
var orgLauncher func(env *hostedEnv, oa *app, ix *store.Index) (handlers.Launcher, handlers.WorkerAPI)

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		return handlers.NewOrgsHandler(env.base.cfg.Server.Mode, &orgService{env: env}, env.audit), nil
	})
}

// seedBenchmarks is the benchmarks folder new orgs get the practice suite
// (coding-core) from: the server's own, but only with HEIMDALL_DEV set
// (tests and development servers). A production hosted org starts with no
// benchmark of its own: it adds its own or uses the built-in library.
func seedBenchmarks(cfg *lib.Config) string {
	if !benchcatalog.DevFlag() {
		return ""
	}
	return cfg.Path(cfg.BenchmarksRoot)
}

// hostedLayout is the folder layout of a hosted server.
func hostedLayout(cfg *lib.Config) tenancy.Layout {
	return tenancy.Layout{Hosted: true, StorageRoot: cfg.Path(cfg.RunsRoot)}
}

// serveHosted serves the hosted platform until ctx ends.
func serveHosted(ctx context.Context, a *app, offline bool) int {
	go logPublicDNS(ctx, a.logger)
	if offline {
		a.logger.Warn("-offline changes nothing in hosted mode: the hosted server runs no agent and opens no gateway; runners do")
	}
	env, closeEnv, err := newHostedEnv(ctx, a, offline)
	if err != nil {
		return fail("hosted: %v", err)
	}
	defer closeEnv()
	// Lane X16: org deletions, retention and the webhook outbox (dataops.go).
	startHostedDataOps(ctx, env)
	startEvidenceMirror(ctx, a)                             // Evidence storage area (P6): mirror and sweeper over runs/orgs/*; seals notify through evidenceOnSeal (evidence.go)
	serveMetrics(ctx, a.cfg.Server.MetricsListen, a.logger) // N9: Prometheus on the admin address (servemetrics.go)
	srv, err := newHostedServer(env)
	if err != nil {
		return fail("server: %v", err)
	}
	a.logger.Info("heimdall hosted platform on http://%s (public url %s, mode %s)", a.cfg.Server.Listen, a.cfg.Auth.PublicURL, a.cfg.Server.Mode)
	go warmLibrary(ctx, a, env) // L1: library suite summaries, one pack at a time beside the listener (warmlibrary_hosted.go)
	if err := srv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
		return fail("serve: %v", err)
	}
	return exitOK
}

// newHostedEnv opens and migrates the database and builds the auth service
// and the tenancy registry.
func newHostedEnv(ctx context.Context, a *app, offline bool) (*hostedEnv, func(), error) {
	d, err := db.Open(ctx, a.cfg.DatabaseConfig())
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	if err := d.Migrate(ctx); err != nil {
		d.Close()
		return nil, nil, fmt.Errorf("migrate database: %w", err)
	}
	authCfg, err := auth.ConfigFrom(a.cfg, "")
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	// Verification emails go through the same transport as notifications
	// (bench/mail, HEIMDALL_SMTP_URL or a Resend or Postmark key). With
	// none, SendMail stays nil: auth writes the links to the server log and
	// tells the user to check with their admin.
	var sendMail auth.MailSender
	if sender, merr := mail.FromEnv(a.logger); merr != nil {
		a.logger.Error("email is not configured correctly; verification links go to the server log only", "error", merr.Error())
	} else if mail.Delivers(sender) {
		sendMail = mail.Func(sender)
	}
	svc, err := auth.New(authCfg, auth.Deps{DB: d, Logger: a.logger, SendMail: sendMail})
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	env := newEnv(ctx, a, d, offline)
	env.authCfg, env.auth = authCfg, svc
	return env, func() {
		stopHostedHub(env) // the runner hands its jobs back through d first
		hostedHubs.Delete(env)
		_ = env.registry.Close()
		d.Close()
	}, nil
}

// newEnv is the database-backed part of hostedEnv (tests reuse it with a
// fake auth middleware).
func newEnv(ctx context.Context, a *app, d *db.DB, offline bool) *hostedEnv {
	env := &hostedEnv{ctx: ctx, base: a, db: d, offline: offline, layout: hostedLayout(a.cfg)}
	env.dir = tenancy.DBDirectory{DB: d}
	env.audit = loggedAudit{sink: tenancy.DBAudit{DB: d}, logger: a.logger}
	env.registry = tenancy.NewRegistry(env.layout, orgBuilder(env), tenancy.Options{})
	return env
}

// newHostedServer assembles the hosted server from env.
func newHostedServer(env *hostedEnv) (*server.Server, error) {
	var areas []server.RouteRegistrar
	for _, mk := range hostedAreas {
		r, err := mk(env)
		if err != nil {
			return nil, err
		}
		if r != nil {
			areas = append(areas, r)
		}
	}
	env.platformRoutes = server.PlatformRoutes(env.base.cfg, areas) // listed in each org's OpenAPI document (audit 8-C-05)
	mw := env.authMW
	if mw == nil {
		mw = auth.Middleware(env.authCfg, env.auth)
	}
	srv, err := server.NewHosted(env.base.cfg, server.Hosted{
		Auth: mw, Directory: env.dir, Registry: env.registry, Audit: env.audit, Platform: areas,
		Ready: readyChecks(env.base), UI: builtUI(), Logger: env.base.logger, Version: benchVersion,
		ClientIP: clientIPOf(env.auth),
		Limits:   apiRateLimits(env.base.logger), // lane X16: per-token and per-org API rate limits (dataops.go)
	})
	if err == nil {
		env.server = srv
	}
	return srv, err
}

// orgBuilder builds one org's runtime: the org's app (its store, project
// root and registry), orgDeps over it, and the org router.
func orgBuilder(env *hostedEnv) tenancy.Builder {
	return func(ctx context.Context, org tenancy.Org, p tenancy.OrgPaths) (*tenancy.OrgRuntime, error) {
		// Seeds the built-in suite on first build: orgs created by signup,
		// GitHub sign-in or `user create` get it here (tenancy.EnsureProject).
		if err := tenancy.EnsureProject(p, seedBenchmarks(env.base.cfg)); err != nil {
			return nil, err
		}
		if err := runOrgInitHooks(p); err != nil { // web parity (servicehooks.go)
			return nil, err
		}
		oa, err := env.base.forOrg(org, p)
		if err != nil {
			return nil, err
		}
		oa.declaredGPUs = hostedDeclaredGPUs(env, oa) // Z05: plan GPU tasks from the org's GPU workers and GPU platform (gpuplan_hosted.go)
		if env.db != nil {
			oa.results = env.db.OrgIndex(org.ID, oa.store, oa.logger) // the org's rows of the database index
		}
		deps, ix, err := orgDeps(ctx, oa)
		if err != nil {
			return nil, err
		}
		deps.UI, deps.Doctor = nil, nil // the UI is served once; preflight checks describe the server, not an org
		hostedOrgFeatures(oa, &deps)
		deps.PlatformRoutes = env.platformRoutes
		// Always wired, -offline or not: the hosted launcher runs no slot
		// itself and needs no gateway; without it runners get a 503.
		if orgLauncher != nil {
			deps.Launcher, deps.Workers = orgLauncher(env, oa, ix)
		}
		if l, ok := deps.Launcher.(*launcher); ok { // improvement runs start on hosted too, through the org's launcher
			deps.Improvements = serveHostedImprovements(oa, l)
		}
		applyServiceHooks(oa, &deps, org.ID, true) // web parity: deps.Services (servicehooks.go)
		if deps.Services.Jobs == nil {
			// Hosted jobs run on the platform hub; the org areas that queue
			// one (catalog import, schedules) reach it through this adapter.
			deps.Services.Jobs = hostedJobsAPI{env: env}
		}
		stopTicker := startHostedScheduleTicker(oa, deps.Services) // G14: stops with the runtime
		r := server.NewOrgRouter(oa.cfg, org.ID, deps)
		rt := &tenancy.OrgRuntime{Handler: r.Handler, Routes: r.Routes, Close: func() error {
			stopTicker()
			r.Close()
			// The jobs hub keeps the launcher (and its index) of this
			// runtime only while the runtime lives (4-B-12).
			if d, ok := deps.Launcher.(interface{ Detach() }); ok {
				d.Detach()
			}
			return nil
		}}
		if ar, ok := deps.Launcher.(interface{ Busy() bool }); ok {
			rt.Busy = ar.Busy
		}
		rt.Value = deps.Launcher
		return rt, nil
	}
}

// hostedOrgFeatures wires the extension feature sources that work over an
// org's stored data alone, each built over the org's own app (audit 8-C-01):
// inbox, judge calibration, export, compliance pack and monitor
// certificates in full; canaries and live scoring as offline sources, so
// their stored status reads work while a canary run or a live pull (which
// would run agents or call a gateway from the coordinator) is refused here
// and denied by tenancy/policy.go before it reaches the handler.
func hostedOrgFeatures(oa *app, deps *server.Deps) {
	deps.Inbox = serveInbox(oa)
	deps.Canaries = serveCanaries(oa, true)
	deps.Improvements = serveImprovements(oa, true) // improvement runs: reads only until the org's launcher is wired (serveHostedImprovements)
	deps.Calibration = judgeSvc{a: oa}
	deps.Export = serveExport{a: oa}
	deps.Livescore = serveLivescore(oa, true)
	deps.Compliance = serveCompliance{a: oa}
	deps.MonitorCerts = serveMonitorCerts(oa)
}

// forOrg returns the app of one hosted org: a's instance config and
// registry with the org's store, project root, suites and release log. The
// copy shares nothing writable with a or with another org's app.
func (a *app) forOrg(org tenancy.Org, p tenancy.OrgPaths) (*app, error) {
	if err := tenancy.ValidOrgID(org.ID); err != nil {
		return nil, err
	}
	cfg := *a.cfg
	cfg.Root = p.ProjectRoot
	cfg.RunsRoot = p.StoreRoot
	cfg.BenchmarksRoot = tenancy.BenchmarksDir
	cfg.Pricing = a.cfg.Path(a.cfg.Pricing)
	st, err := store.NewFSStore(p.StoreRoot, a.logger)
	if err != nil {
		return nil, fmt.Errorf("open org store: %w", err)
	}
	reg := *a.reg
	library := suite.LibraryDir() // the server-wide built-in benchmark layer, read-only (suite/library.go)
	reg.Loaders = suite.Layered(p.BenchmarksRoot(), suite.LibraryBenchmarks(library))
	reg.Adapters = adapters.Builtin(adapters.CommandConfig{
		GatewayAgentURL: cfg.Gateway.AgentURL, EvalNetwork: cfg.Networks.Eval, BenchRoot: p.ProjectRoot,
		GatewayPublicURL: cfg.Gateway.PublicURL,
		OrgID:            org.ID, PullURL: cfg.PullRunURL(), // Y11 pull-mode agents
	})
	reg.Store = st
	oa := &app{cfg: &cfg, logger: a.logger, docker: a.docker, reg: &reg, store: st, upstreams: a.upstreams, prices: a.prices,
		orgID: org.ID, releasesRoot: p.ReleasesRoot, library: library}
	project := p.ProjectRoot
	oa.planGuard = func(e *schemas.Experiment) error { return tenancy.CheckExperimentPathsWithLibrary(project, library, e) }
	oa.planConfine = project
	oa.deferGateway = true                                                 // the hosted server opens no gateway: manifests record gateway.deferred_to_runner
	reg.Secrets = orgSecretSource{keys: sharedKeyStore(oa), orgID: org.ID} // G01: the org's own secrets, never the server's environment
	return oa, nil
}

// resultsSource is the results index of a's root: the org's database index
// when one is wired (a.results), else the JSONL index beside the store.
func (a *app) resultsSource() resultsindex.Source {
	if a.results != nil {
		return a.results
	}
	return resultsindex.New(a.store, a.logger)
}

// hostedConfig reports whether the config at path says auth.mode hosted. An
// unreadable config is not hosted; newApp reports the error.
func hostedConfig(path string) bool {
	cfg, err := loadConfig(path)
	return err == nil && cfg.Auth.IsHosted()
}

// serveReleases is the release log of a's root: release.DefaultRoot in
// local mode (serveReleaseLog), the org's own folder for a hosted org, never
// BENCH_RELEASES_ROOT, which would share one log across orgs.
func (a *app) serveReleases() handlers.ReleaseLog {
	if a.releasesRoot == "" {
		return serveReleaseLog(a)
	}
	l, err := release.New(release.Deps{Store: a.store, Root: a.releasesRoot, Logger: a.logger})
	if err != nil {
		a.logger.Warn("release log unavailable", "org", a.orgID, "error", err.Error())
		return nil
	}
	return l
}

// loggedAudit logs a failed audit write instead of failing the request.
type loggedAudit struct {
	sink   tenancy.AuditSink
	logger schemas.Logger
}

func (l loggedAudit) Audit(ctx context.Context, e tenancy.AuditEntry) error {
	err := l.sink.Audit(ctx, e)
	if err != nil {
		l.logger.Error("audit write failed", "org", e.OrgID, "action", e.Action, "error", err.Error())
	}
	return err
}

// orgService adapts the database and auth areas to handlers.OrgService.
type orgService struct{ env *hostedEnv }

var _ handlers.OrgService = (*orgService)(nil)

func orgInfo(o *db.Org, role string) handlers.OrgInfo {
	return handlers.OrgInfo{ID: o.ID, Slug: o.Slug, Name: o.Name, PublicRead: o.PublicRead, CreatedAt: o.CreatedAt, Role: role}
}

// dbErr maps database errors onto the handlers' sentinels.
func dbErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, db.ErrNotFound):
		return fmt.Errorf("%w: %v", schemas.ErrNotFound, err)
	case errors.Is(err, db.ErrConflict):
		return fmt.Errorf("%w: %v", schemas.ErrExists, err)
	case errors.Is(err, db.ErrLastOwner):
		return handlers.ErrLastOwner
	}
	return err
}

// CreateOrg creates the org with its owner, then its folders (with the
// practice suite only on a development server, seedBenchmarks).
func (s *orgService) CreateOrg(ctx context.Context, slug, name, owner string) (handlers.OrgInfo, error) {
	o, err := createOrg(ctx, s.env.db, s.env.layout, s.env.base.cfg, slug, name, owner, false)
	if err != nil {
		return handlers.OrgInfo{}, err
	}
	return orgInfo(o, string(auth.RoleOwner)), nil
}

// createOrg is CreateOrg for the API and the CLI. The folders come first,
// under a fresh org ID, and the org row only after they exist: a filesystem
// failure then leaves no committed org (and no taken slug) behind, and a
// database failure removes the folders it staged.
func createOrg(ctx context.Context, d *db.DB, layout tenancy.Layout, cfg *lib.Config, slug, name, owner string, public bool) (*db.Org, error) {
	if !db.ValidSlug(slug) {
		return nil, fmt.Errorf("%w: slug %q", handlers.ErrOrgInput, slug)
	}
	id := db.NewID("org")
	p, err := layout.Paths(id)
	if err != nil {
		return nil, err
	}
	unstage := func() {
		for _, dir := range []string{p.StoreRoot, p.ProjectRoot} {
			if dir != "" {
				_ = os.RemoveAll(dir)
			}
		}
	}
	if err := tenancy.EnsureProject(p, seedBenchmarks(cfg)); err != nil {
		unstage()
		return nil, fmt.Errorf("create the folders of org %s: %w", slug, err)
	}
	o, err := d.CreateOrg(ctx, db.NewOrg{ID: id, Slug: slug, Name: name, OwnerID: owner, PublicRead: public})
	if err != nil {
		unstage()
		return nil, dbErr(err)
	}
	return o, nil
}

func (s *orgService) OrgsOf(ctx context.Context, userID string) ([]handlers.OrgInfo, error) {
	rows, err := s.env.db.OrgsForUser(ctx, userID)
	if err != nil {
		return nil, dbErr(err)
	}
	out := make([]handlers.OrgInfo, 0, len(rows))
	for i := range rows {
		out = append(out, orgInfo(&rows[i].Org, rows[i].Role))
	}
	return out, nil
}

func (s *orgService) Org(ctx context.Context, orgID string) (handlers.OrgInfo, error) {
	o, err := s.env.db.OrgByID(ctx, orgID)
	if err != nil {
		return handlers.OrgInfo{}, dbErr(err)
	}
	return orgInfo(o, ""), nil
}

func (s *orgService) RenameOrg(ctx context.Context, orgID, name string) (handlers.OrgInfo, error) {
	if err := s.env.db.RenameOrg(ctx, orgID, name); err != nil {
		return handlers.OrgInfo{}, dbErr(err)
	}
	return s.Org(ctx, orgID)
}

func (s *orgService) SetPublicRead(ctx context.Context, orgID string, on bool) (handlers.OrgInfo, error) {
	if err := s.env.db.SetOrgPublicRead(ctx, orgID, on); err != nil {
		return handlers.OrgInfo{}, dbErr(err)
	}
	return s.Org(ctx, orgID)
}

func memberInfo(m db.Member) handlers.MemberInfo {
	return handlers.MemberInfo{UserID: m.UserID, Email: m.Email, Name: m.Name, Role: m.Role, CreatedAt: m.CreatedAt}
}

func (s *orgService) Members(ctx context.Context, orgID string) ([]handlers.MemberInfo, error) {
	ms, err := s.env.db.Members(ctx, orgID)
	if err != nil {
		return nil, dbErr(err)
	}
	out := make([]handlers.MemberInfo, 0, len(ms))
	for _, m := range ms {
		out = append(out, memberInfo(m))
	}
	return out, nil
}

func (s *orgService) Member(ctx context.Context, orgID, userID string) (handlers.MemberInfo, error) {
	ms, err := s.env.db.Members(ctx, orgID)
	if err != nil {
		return handlers.MemberInfo{}, dbErr(err)
	}
	for _, m := range ms {
		if m.UserID == userID {
			return memberInfo(m), nil
		}
	}
	return handlers.MemberInfo{}, fmt.Errorf("%w: member %s", schemas.ErrNotFound, userID)
}

func (s *orgService) SetMemberRole(ctx context.Context, orgID, userID string, role auth.Role, g handlers.MemberGuard) (handlers.MemberInfo, auth.Role, error) {
	from, err := s.env.db.SetRoleChecked(ctx, orgID, userID, string(role), memberChange(g))
	if err != nil {
		return handlers.MemberInfo{}, "", dbErr(err)
	}
	m, err := s.Member(ctx, orgID, userID)
	return m, auth.Role(from), err
}

func (s *orgService) RemoveMember(ctx context.Context, orgID, userID string, g handlers.MemberGuard) (auth.Role, error) {
	c := memberChange(g)
	c.After = func(ctx context.Context, tx *sql.Tx) error {
		return auth.RevokeMemberTokensTx(ctx, s.env.db, tx, orgID, userID)
	}
	from, err := s.env.db.RemoveMemberChecked(ctx, orgID, userID, c)
	return auth.Role(from), dbErr(err)
}

// memberChange runs a handler's member guard, and appends its audit row, in
// the database transaction of the change.
func memberChange(g handlers.MemberGuard) db.MemberChange {
	c := db.MemberChange{ActorID: g.ActorID}
	if g.Check != nil {
		c.Check = func(actor, target string) error { return g.Check(auth.Role(actor), auth.Role(target)) }
	}
	if g.Audit != nil {
		c.Audit = func(from string) *db.AuditEntry {
			e := tenancy.DBEntry(g.Audit(auth.Role(from)))
			return &e
		}
	}
	return c
}

func invitationInfo(in auth.Invitation) handlers.InvitationInfo {
	return handlers.InvitationInfo{ID: in.ID, Email: in.Email, Role: string(in.Role), CreatedBy: in.CreatedBy,
		CreatedAt: in.CreatedAt, ExpiresAt: in.ExpiresAt, AcceptedAt: in.AcceptedAt}
}

func (s *orgService) Invitations(ctx context.Context, orgID string) ([]handlers.InvitationInfo, error) {
	if s.env.auth == nil {
		return nil, errors.New("auth service unavailable")
	}
	ins, err := s.env.auth.ListInvitations(ctx, orgID)
	if err != nil {
		return nil, dbErr(err)
	}
	out := make([]handlers.InvitationInfo, 0, len(ins))
	for _, in := range ins {
		out = append(out, invitationInfo(in))
	}
	return out, nil
}

func (s *orgService) CreateInvitation(ctx context.Context, inviter *auth.Principal, orgID, email string, role auth.Role, ip string) (handlers.InvitationInfo, error) {
	if s.env.auth == nil {
		return handlers.InvitationInfo{}, errors.New("auth service unavailable")
	}
	c, err := s.env.auth.CreateInvitation(ctx, inviter, orgID, email, role, ip)
	if err != nil {
		return handlers.InvitationInfo{}, err
	}
	out := invitationInfo(c.Invitation)
	out.Token, out.URL = c.Token, c.URL
	return out, nil
}

func (s *orgService) RevokeInvitation(ctx context.Context, p *auth.Principal, orgID, id, ip string) error {
	if s.env.auth == nil {
		return errors.New("auth service unavailable")
	}
	return s.env.auth.RevokeInvitation(ctx, p, orgID, id, ip)
}

func (s *orgService) Audit(ctx context.Context, orgID string, q handlers.AuditQuery) ([]handlers.AuditRow, string, error) {
	f := db.AuditFilter{OrgID: orgID, Action: q.Action, Until: q.Before, Cursor: q.Cursor, Limit: q.Limit}
	page, err := s.env.db.ListAudit(ctx, f)
	if err != nil {
		if errors.Is(err, db.ErrBadCursor) {
			return nil, "", fmt.Errorf("%w: cursor: %v", handlers.ErrOrgInput, err)
		}
		return nil, "", dbErr(err)
	}
	out := make([]handlers.AuditRow, 0, len(page.Entries))
	for _, e := range page.Entries {
		out = append(out, handlers.AuditRow{ID: e.ID, ActorType: e.ActorType, ActorID: e.ActorID, Action: e.Action,
			TargetType: e.TargetType, TargetID: e.TargetID, IP: e.IP, At: e.At, Details: e.Details})
	}
	return out, page.Next, nil
}

// clientIPOf is the auth service's trusted-proxy client address for audit
// rows (4-B-08), or nil without a service.
func clientIPOf(svc *auth.Service) func(*fasthttp.RequestCtx) string {
	if svc == nil {
		return nil
	}
	return svc.ClientIP
}
