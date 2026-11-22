// Package server assembles the bench HTTP server: every handler of the
// /api/bench API, the built UI with SPA fallback, the middlewares, and a
// graceful shutdown. It mirrors transports/bifrost-http/server in Bifrost.
package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/maximhq/bifrost/bench/selfmetrics"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/packs"
	"github.com/maximhq/bifrost/bench/reference"
	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskhealth"
	"github.com/valyala/fasthttp"
)

// Server timeouts. There is no write timeout: server-sent event streams stay
// open for the whole experiment.
const (
	readTimeout     = 30 * time.Second
	idleTimeout     = 2 * time.Minute
	shutdownTimeout = 10 * time.Second
)

// Deps are the implementations cmd/bifrost-bench wires in. Any of them may be
// nil; the routes that need a missing one answer 503.
type Deps struct {
	Index    handlers.ReadIndex
	Analyzer handlers.Analyzer
	Launcher handlers.Launcher
	Catalog  handlers.CatalogSource
	Auditor  handlers.Auditor
	Doctor   handlers.DoctorFunc
	Releases handlers.ReleaseLog
	Suites   handlers.SuiteSource
	Agents   handlers.AgentSource
	Workers  handlers.WorkerAPI // Remote area: remote worker coordinator; nil turns worker mode off (handlers/workers.go)
	// Index and trends area: trends over the derived results index; nil answers 503 (handlers/trends.go).
	Trends handlers.TrendSource
	// Human review area: review queue, review history and new reviews; nil answers 503 (handlers/reviews.go).
	Reviews handlers.ReviewSource
	// Failure inbox area: inbox items, accept/reject/snooze and the webhook; nil answers 503 (handlers/inbox.go).
	Inbox handlers.InboxSource
	// W8 judge calibration: calibration card and labels of judged suites; nil answers 503 (handlers/calibration.go).
	Calibration handlers.CalibrationSource
	// W5 drift canary: canaries, their looks and a run behind the write token; nil answers 503 (handlers/canary.go).
	Canaries handlers.CanarySource
	// Improvement runs: list, report, lineage, create, approve and stop behind the write token; nil answers 503 (handlers/improve.go).
	Improvements handlers.ImproveSource
	// Forecast and matrix area: POST /estimate and GET /experiments/{id}/matrix; nil answers 503 (handlers/forecast.go).
	Forecast handlers.ForecastSource
	// W6 migration report and cost explanation: GET /experiments/{id}/migration, /cost-explain and /pareto; nil answers 503 (handlers/migration.go, handlers/costexplain.go).
	Migration   handlers.MigrationSource
	CostExplain handlers.CostExplainSource
	// W10 export: GET /experiments/{id}/export and /suites/{id}/export; nil answers 503 (handlers/export.go).
	Export handlers.ExportSource
	// X6 live scoring: status, sessions, comparison and a pull behind the write token; nil answers 503 (handlers/livescore.go).
	Livescore handlers.LivescoreSource
	// Suite health area: sealed runs for GET /suites/{id}/health; nil answers 503 (handlers/suitehealth.go).
	Health taskhealth.Source
	Jobs   handlers.JobsAPI      // Jobs area (P4): the job API in local mode; hosted mode serves it as a platform area (handlers/jobs.go)
	Ready  []handlers.ReadyCheck // Deploy area: /readyz checks (database, migrations, runs root); /healthz needs none (handlers/readiness.go)
	// Eval integrity area (S2): compliance packs for GET /experiments/{id}/compliance; nil answers 503 (handlers/compliance.go).
	Compliance handlers.ComplianceSource
	// Eval integrity area (S2): stored monitor certificates for GET /monitors/certifications; nil answers 503 (handlers/monitorcert.go).
	MonitorCerts handlers.MonitorCertSource
	// UI is the built UI with index.html at its root, for example
	// fs.Sub(ui.Dist, "dist"). Nil serves the API only.
	UI      fs.FS
	Logger  schemas.Logger
	Version string
	// PlatformRoutes are a hosted server's platform routes (accounts, orgs,
	// jobs, runners), described in each org router's OpenAPI document beside
	// the org routes (audit 8-C-05). Nil in local mode.
	PlatformRoutes map[string][]string
	// Web parity: per-org services for the areas registered with
	// handlers.RegisterOrgArea (handlers/orgareas.go); any field may be nil.
	Services handlers.Services
}

// Server is the bench HTTP server.
type Server struct {
	cfg            *lib.Config
	logger         schemas.Logger
	handler        fasthttp.RequestHandler
	server         *fasthttp.Server
	experiments    *handlers.ExperimentsHandler
	token          string
	tokenGenerated bool
	announceOnce   sync.Once
	closers        []func() // Tenancy area (P3): hosted mode's org registry and sweeper (hosted.go)
}

// New builds the server and registers every route. The write token comes
// from BENCH_WRITE_TOKEN or is generated here and logged once when serving
// starts.
func New(cfg *lib.Config, deps Deps) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("new server: config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("new server: %w", err)
	}
	token, generated, err := lib.ResolveWriteToken()
	if err != nil {
		return nil, fmt.Errorf("new server: %w", err)
	}
	logger := deps.Logger
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	s := &Server{cfg: cfg, logger: logger, token: token, tokenGenerated: generated}
	mode := cfg.Server.Mode
	write := lib.RequireWriteToken(mode, token)
	common := []lib.Middleware{lib.BodyLimit(mode, lib.MaxBodyBytes)}

	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found: "+string(ctx.Path()))
	}
	r.MethodNotAllowed = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, mode, fasthttp.StatusMethodNotAllowed, lib.ErrTypeMethodNotAllowed,
			"method "+string(ctx.Method())+" not allowed on "+string(ctx.Path()))
	}
	s.experiments = registerOrgRoutes(r, cfg, deps, write, common) // Tenancy area (P3): the same registration builds every hosted org router (hosted.go)
	// Jobs area (P4): /jobs routes of the local org; nil Jobs answers 503 (handlers/jobs.go).
	jobsHandler := handlers.NewJobsHandler(mode, deps.Jobs, write)
	jobsHandler.RegisterRoutes(r, common...)
	s.closers = append(s.closers, jobsHandler.Close)
	handlers.NewAuthHandler(mode, nil).RegisterRoutes(r, common...)                 // Auth area (P2): local mode answers GET /auth/config and /auth/me (local principal); hosted-only routes 404 (handlers/auth.go)
	handlers.NewRunnersHandler(mode, nil, nil).RegisterRoutes(r, common...)         // Runners area (P5): runner admin is hosted only; local mode answers 503 (handlers/runners.go)
	handlers.NewWorkerPlatformsHandler(mode, nil, nil).RegisterRoutes(r, common...) // X19a: cloud sandbox platforms are hosted only; local mode answers 503
	handlers.NewOpenModelsHandler(mode, nil, nil).RegisterRoutes(r, common...)      // Z05: open models on the org's Modal account are hosted only; local mode answers 503
	// Bring your own worker: hosted only; local mode answers 503 (handlers/workerconns.go).
	handlers.NewWorkerConnsHandler(mode, nil, handlers.WorkerConnErrors{}, nil).RegisterRoutes(r, common...)
	handlers.NewWorkerTargetHandler(mode, nil, nil).RegisterRoutes(r, common...) // where runs go: hosted only; local mode answers 503 (handlers/workertarget.go)
	// G4-03: the public reference board (handlers/reference.go).
	handlers.NewReferenceHandler(mode, reference.Open(cfg.Path(cfg.RunsRoot))).RegisterRoutes(r, common...)
	// Y11 pull-mode agents: the local runs' launchers (handlers/pullagent.go); the agent side is an org route (registerOrgRoutes).
	handlers.NewPullAgentHandler(mode, pullagent.Shared(), pullagent.DefaultSigner(), false, cfg.PullPublicOrigin(), write).RegisterRunRoutes(r, common...)
	if deps.UI != nil {
		NewUIHandler(mode, deps.UI).RegisterRoutes(r)
	}

	// Host check, CORS and security headers wrap the router itself, so they
	// cover the UI, the 404 answer and every API route alike.
	s.handler = lib.ChainMiddlewares(r.Handler,
		lib.SecurityHeaders(),
		lib.HostGuard(mode, cfg.Server.AllowedHosts),
		lib.CORS(cfg.Server.CORSOrigins),
		auth.Middleware(auth.Config{ServerMode: mode}, nil), // Auth area (P2): every local request carries the local-org owner principal (docs/auth.md)
		handlers.V1Alias(mode, deps.Version, false),         // Public API v1: /api/v1 rewritten onto its bench routes (handlers/apiv1.go, docs/api-v1.md)
	)
	s.server = &fasthttp.Server{
		Handler:               selfmetrics.Default.Middleware(s.handler), // N9: API latency for /metrics
		Name:                  "bifrost-bench",
		MaxRequestBodySize:    lib.MaxUploadBytes, // per-route limits: lib.BodyLimit in common/upload middlewares
		ErrorHandler:          lib.RequestErrorHandler(mode, lib.MaxUploadBytes),
		ReadTimeout:           readTimeout,
		IdleTimeout:           idleTimeout,
		NoDefaultServerHeader: true,
		Logger:                fasthttpLogger{logger},
	}
	return s, nil
}

// registerOrgRoutes registers the bench API over one run store on r: every
// route of local mode, and of each organization's router in hosted mode
// (Tenancy area, hosted.go). Register new org-scoped routes here and give
// them a policy entry in tenancy/policy.go (TestPolicyCoversEveryRoute).
func registerOrgRoutes(r *router.Router, cfg *lib.Config, deps Deps, write lib.Middleware, common []lib.Middleware) *handlers.ExperimentsHandler {
	mode := cfg.Server.Mode
	experiments := handlers.NewExperimentsHandler(mode, deps.Index, deps.Analyzer, deps.Launcher, deps.Catalog, write)
	handlers.NewHealthHandler(mode, deps.Version, deps.Doctor).WithLaunch(deps.Launcher != nil).RegisterRoutes(r, common...)
	handlers.NewReadinessHandler(mode, deps.Version, deps.Ready).RegisterRoutes(r, common...) // Deploy area: GET /healthz and /readyz at the site root (handlers/readiness.go)
	handlers.NewCatalogHandler(mode, deps.Catalog).RegisterRoutes(r, common...)
	experiments.RegisterRoutes(r, common...)
	// Y11 pull-mode agents, the serving process's side (handlers/pullagent.go). On a hosted
	// server the platform router serves these routes first (cmd/bifrost-bench/pullagent_hosted.go).
	handlers.NewPullAgentHandler(mode, pullagent.Shared(), pullagent.DefaultSigner(), false, cfg.PullPublicOrigin(), write).RegisterAgentRoutes(r, common...)
	// Held-out tasks: a held-out task's run content needs held_out.visible too.
	handlers.NewRunsHandler(mode, deps.Index).WithSuites(deps.Suites).WithHeldOutVisible(cfg.HeldOut.Visible).RegisterRoutes(r, common...)
	handlers.NewRunListHandler(mode, deps.Index, deps.Suites).WithRoot(cfg.Root).RegisterRoutes(r, common...) // UI regroup (U2): GET /runs (handlers/runlist.go)
	handlers.NewPairsHandler(mode, deps.Index).WithHeldOutVisible(cfg.HeldOut.Visible).RegisterRoutes(r, common...)
	handlers.NewTriageHandler(mode, deps.Index, deps.Catalog).RegisterRoutes(r, common...)
	// U3: the held-out board needs held_out.visible, as held-out task detail does.
	handlers.NewLeaderboardHandler(mode, deps.Index).WithHeldOutVisible(cfg.HeldOut.Visible).WithAgentBoard(cfg.Root, deps.Suites).WithKeyPrices(deps.Services.Keys, deps.Services.OrgID).RegisterRoutes(r, common...)
	handlers.NewClustersHandler(mode, deps.Index).RegisterRoutes(r, common...) // Failure clusters area (handlers/clusters.go)
	handlers.NewAuditHandler(mode, deps.Auditor, write).RegisterRoutes(r, common...)
	handlers.NewReleasesHandler(mode, deps.Releases, write).RegisterRoutes(r, common...)
	suitesHandler := handlers.NewSuitesHandler(mode, deps.Suites).WithHeldOutVisible(cfg.HeldOut.Visible).WarmCache()
	if hosted := cfg.Auth.IsHosted(); !benchcatalog.DevContent(hosted) { // Acme practice examples: local development only; the practice suite: never in a hosted org
		examples := benchcatalog.ExampleSuiteIDs(benchcatalog.SourceRoot())
		suitesHandler.WithHiddenSuites(func(id string) bool { return examples[id] || benchcatalog.HidePracticeSuite(hosted, id) })
	}
	suitesHandler.RegisterRoutes(r, common...) // Suite health area: held_out.visible; WarmCache: suite list cache
	var packSuites packs.SuiteLister           // nil without a suite loader: GET /evaluation-packs answers 503
	if deps.Suites != nil {
		packSuites = suitesHandler
	}
	packs.NewHandler(mode, packSuites).RegisterRoutes(r, common...)                             // A03: GET /evaluation-packs (packs/handler.go)
	handlers.NewSuiteHealthHandler(mode, deps.Suites, deps.Health).RegisterRoutes(r, common...) // Suite health area (handlers/suitehealth.go)
	// Scripted controls (oracle, noop) are hidden from users except in local development (benchcatalog.DevContent).
	// The Acme practice example agents (acme-agent, acme-solo, acme-team and
	// any file copied from examples/acme-*) are hidden unless HEIMDALL_DEV is
	// set; an org's real agents stay.
	agentsHandler := handlers.NewAgentsHandler(mode, deps.Agents, deps.Index).WithHiddenControls(!benchcatalog.DevContent(cfg.Auth.IsHosted()))
	if !benchcatalog.DevFlag() {
		exampleAgents := benchcatalog.NewExampleAgents(benchcatalog.SourceRoot())
		agentsHandler.WithHiddenAgents(func(f handlers.AgentConfigFile) bool {
			id := ""
			if f.Config != nil {
				id = f.Config.ID
			}
			var content []byte
			if cfg.Root != "" && f.Path != "" {
				content, _ = os.ReadFile(filepath.Join(cfg.Root, filepath.FromSlash(f.Path)))
			}
			return exampleAgents.Match(id, content)
		})
	}
	agentsHandler.RegisterRoutes(r, common...)
	// Agent diff area: two versions of an agent config compared (handlers/agentdiff.go, docs/agent-diff.md).
	handlers.NewAgentDiffHandler(mode, deps.Index, cfg.Root).RegisterRoutes(r, common...)
	handlers.NewImportsHandler(mode).RegisterRoutes(r, common...) // UI area: importer catalog (handlers/imports.go)
	// Forecast and matrix area: POST /estimate and GET /experiments/{id}/matrix (handlers/forecast.go, docs/forecast.md).
	handlers.NewForecastHandler(mode, deps.Forecast, write).RegisterRoutes(r, common...)
	// Remote area: worker API, off unless BENCH_WORKER_TOKEN is set (handlers/workers.go).
	handlers.NewWorkersHandler(mode, deps.Workers).RegisterRoutes(r, common...)
	openapi := handlers.NewOpenAPIHandler(mode, deps.Version)
	if cfg.Auth.IsHosted() { // Tenancy area: the hosted document lists the platform routes and hosted auth (audit 8-C-05)
		openapi.ForHosted(deps.PlatformRoutes)
	}
	openapi.RegisterRoutes(r, common...) // Clients area: API description (handlers/openapi.go, docs/clients.md)
	// Index and trends area: GET /trends over the derived results index (handlers/trends.go, docs/trends.md).
	handlers.NewTrendsHandler(mode, deps.Trends).WithHeldOutVisible(cfg.HeldOut.Visible).RegisterRoutes(r, common...)
	// Human review area: review queue and history, POST /reviews behind the write token (handlers/reviews.go, docs/human-review.md).
	handlers.NewReviewsHandler(mode, deps.Reviews, write).RegisterRoutes(r, common...)
	handlers.NewInboxHandler(mode, deps.Inbox, write).RegisterRoutes(r, common...)                                    // Failure inbox area (handlers/inbox.go, docs/failure-inbox.md)
	handlers.NewRoutingHandler(mode, deps.Index).WithHeldOutVisible(cfg.HeldOut.Visible).RegisterRoutes(r, common...) // W9 routing policy under test (handlers/routing.go)
	handlers.NewSupportHandler(mode, deps.Index).RegisterRoutes(r, common...)                                         // W3 customer support scorecard (handlers/support.go)
	handlers.NewResearchHandler(mode, deps.Index).RegisterRoutes(r, common...)                                        // W4 research with Tavily scorecard (handlers/research.go)
	// W7 adaptive trials and suite tiers: the trials and tier panel (handlers/adaptive.go, docs/adaptive-trials.md).
	handlers.NewAdaptiveHandler(mode, deps.Index, deps.Analyzer).RegisterRoutes(r, common...)
	// W8 judge calibration: calibration card and labels (handlers/calibration.go, docs/judge-calibration.md).
	handlers.NewCalibrationHandler(mode, deps.Calibration, write).WithHeldOut(deps.Suites, cfg.HeldOut.Visible).RegisterRoutes(r, common...)
	// W5 drift canary: GET /canaries, GET /canaries/{name}, POST /canaries/{name}/run (handlers/canary.go, docs/drift-canary.md).
	handlers.NewCanaryHandler(mode, deps.Canaries, write).RegisterRoutes(r, common...)
	// Improvement runs: /improvements (handlers/improve.go, docs/improvement-runs.md).
	handlers.NewImproveHandler(mode, deps.Improvements, write).RegisterRoutes(r, common...)
	// W6 migration report, cost explanation and Pareto planes (handlers/migration.go, handlers/costexplain.go, docs/model-migration.md, docs/cost-explain.md).
	handlers.NewMigrationHandler(mode, deps.Migration).RegisterRoutes(r, common...)
	// U3: the held-out board's plane needs held_out.visible too.
	handlers.NewCostExplainHandler(mode, deps.CostExplain, deps.Index).WithHeldOutVisible(cfg.HeldOut.Visible).RegisterRoutes(r, common...)
	handlers.NewExportHandler(mode, deps.Export).RegisterRoutes(r, common...) // W10 export (handlers/export.go, docs/export.md)
	// X6 live scoring: GET /livescore, /livescore/sessions, /livescore/compare, POST /livescore/pull (handlers/livescore.go, docs/live-scoring.md).
	handlers.NewLivescoreHandler(mode, deps.Livescore, write).RegisterRoutes(r, common...)
	// Eval integrity area (S2): eval awareness scan (handlers/awareness.go).
	handlers.NewAwarenessHandler(mode, deps.Index).RegisterRoutes(r, common...)
	handlers.NewComplianceHandler(mode, deps.Compliance).RegisterRoutes(r, common...) // Eval integrity area (S2): compliance pack (handlers/compliance.go)
	// Eval integrity area (S2): monitor certification (handlers/monitorcert.go).
	handlers.NewMonitorCertHandler(mode, deps.MonitorCerts).RegisterRoutes(r, common...)
	// Web parity: every area registered with handlers.RegisterOrgArea (handlers/orgareas.go).
	env := handlers.OrgEnv{Cfg: cfg, Mode: mode, Services: deps.Services, Write: write, Common: common, Upload: uploadMiddlewares(mode, common)}
	for _, area := range handlers.OrgAreas() {
		area(env, r)
	}
	return experiments
}

// uploadMiddlewares is common with its trailing body limit raised to
// lib.MaxUploadBytes (the body limit is always common's last middleware).
func uploadMiddlewares(mode schemas.Mode, common []lib.Middleware) []lib.Middleware {
	if len(common) == 0 {
		return nil
	}
	out := append([]lib.Middleware{}, common[:len(common)-1]...)
	return append(out, lib.BodyLimit(mode, lib.MaxUploadBytes))
}

// Handler returns the root handler with every middleware applied.
func (s *Server) Handler() fasthttp.RequestHandler {
	return s.handler
}

// WriteToken returns the token POST routes require.
func (s *Server) WriteToken() string {
	return s.token
}

// ListenAndServe listens on server.listen and serves until ctx ends, then
// shuts down gracefully. A non-loopback address is allowed but warned about:
// loopback binding is the API's first defence.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if !s.cfg.ListenIsLoopback() && !s.cfg.Auth.IsHosted() { // Tenancy area: hosted mode is meant to listen behind its proxy; sessions and tokens guard it
		s.logger.Warn(fmt.Sprintf("bench API listens on %s, which is not loopback; only the write token guards it", s.cfg.Server.Listen))
	}
	ln, err := net.Listen("tcp", s.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Server.Listen, err)
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx ends or the listener fails. On ctx end it
// closes the live event streams first, then waits up to shutdownTimeout for
// open requests.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.announceOnce.Do(func() {
		s.logger.Info(fmt.Sprintf("bench API on http://%s/ (mode %s)", ln.Addr(), s.cfg.Server.Mode))
		if s.tokenGenerated {
			s.logger.Info(fmt.Sprintf("bench write token (shown once; set %s to fix it): %s", lib.WriteTokenEnv, s.token))
		}
	})
	errCh := make(chan error, 1)
	go func() { errCh <- s.server.Serve(ln) }()
	select {
	case err := <-errCh:
		s.closeHandlers()
		if err != nil {
			return fmt.Errorf("serve bench API: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	s.closeHandlers()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := s.server.ShutdownWithContext(shutdownCtx)
	// When ctx ended before the serving goroutine registered ln, the
	// library's shutdown has nothing to close and Serve would then accept
	// on ln forever: close it here too, and bound the wait (QC2-20-05).
	_ = ln.Close()
	select {
	case err := <-errCh:
		if shutdownErr != nil {
			return fmt.Errorf("shut down bench API: %w", shutdownErr)
		}
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("serve bench API: %w", err)
		}
		return nil
	case <-shutdownCtx.Done():
		return fmt.Errorf("shut down bench API: serving did not stop within %s", shutdownTimeout)
	}
}

// closeHandlers closes the live event streams: local mode's experiments
// handler, and in hosted mode every org runtime (Tenancy area, hosted.go).
func (s *Server) closeHandlers() {
	if s.experiments != nil {
		s.experiments.Close()
	}
	for _, c := range s.closers {
		c()
	}
}

// fasthttpLogger routes fasthttp's connection errors (an oversized body, a
// client that hung up) to the bench logger at debug level.
type fasthttpLogger struct{ logger schemas.Logger }

// Printf implements fasthttp.Logger.
func (l fasthttpLogger) Printf(format string, args ...any) {
	l.logger.Debug(fmt.Sprintf(format, args...))
}
