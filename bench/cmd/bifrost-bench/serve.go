package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/attribution"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/ui"
	"github.com/maximhq/bifrost/bench/verdict"
)

func init() {
	register("serve", "serve the web UI and the API over the run folder", runServe)
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	offline := fs.Bool("offline", false, "serve recorded runs only: no gateway, no launching")
	debug := fs.Bool("debug", false, "debug logging")
	extras := addServeExtras(fs) // Index and trends area: trends API and -schedules daemon flag (serveextras.go)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Tenancy area (P3): a hosted server runs no agent, so it needs no
	// gateway: runners hold the gateway and the provider keys (servetenancy.go).
	a, err := newApp(ctx, *config, appOptions{gateway: !*offline && !hostedConfig(*config), debug: *debug})
	if err != nil {
		return fail("%v", err)
	}
	// An offline server runs nothing and reaches no gateway: everything it
	// serves is replayed from disk, so it is never a live answer, whatever
	// bench.yaml says (audit 7-B-03). Synthetic stays synthetic.
	a.cfg.Server.Mode = offlineMode(a.cfg.Server.Mode, *offline)
	// Tenancy area (P3): auth.mode hosted serves every organization from its
	// own folders through the tenancy registry (servetenancy.go).
	if a.cfg.Auth.IsHosted() {
		return serveHosted(ctx, a, *offline)
	}
	// Tenancy area (P3): orgDeps builds the deps over one org root; local
	// mode calls it once, for org local, hosted mode once per org.
	deps, ix, err := orgDeps(ctx, a)
	if err != nil {
		return fail("%v", err)
	}
	if !*offline {
		deps.Launcher = newLauncher(a, ix)
	}
	deps.Launcher, deps.Workers = withWorkers(a, deps.Launcher) // Remote area: worker mode when BENCH_WORKER_TOKEN is set (worker.go)
	// Index and trends area: trends source and optional schedule daemon (serveextras.go).
	if err := extras.apply(ctx, a, &deps, *offline); err != nil {
		return fail("%v", err)
	}
	deps.Inbox = serveInbox(a) // Failure inbox area: inbox page, actions and webhook (inbox.go)
	// W5 drift canary: canary list, detail and run (canary.go).
	deps.Canaries = serveCanaries(a, *offline)
	// W8 judge calibration: calibration card and labels (judge.go).
	deps.Calibration = judgeSvc{a: a}
	deps.Export = serveExport{a: a} // W10 export downloads (export.go)
	// X6 live scoring: Live page and API (livescore.go).
	deps.Livescore = serveLivescore(a)
	deps.Compliance = serveCompliance{a: a} // Eval integrity area (S2): compliance pack (compliance.go)
	// Eval integrity area (S2): monitor certification (monitor.go).
	deps.MonitorCerts = serveMonitorCerts(a)
	// Jobs area (P4): experiments run as experiment.run jobs (cancel,
	// resume after a restart) and the ops kinds serve /jobs (servejobs.go).
	stopJobs := startLocalJobs(ctx, a, *config, deps.Launcher, ix, &deps)
	defer stopJobs()
	deps.Ready = readyChecks(a) // Deploy area: /readyz checks (serveready.go)
	startEvidenceMirror(ctx, a) // Evidence storage area (P6): background mirror and sweeper when storage.mirror_dir or storage.s3 is set; seal hook evidenceOnSeal (evidence.go)
	srv, err := server.New(a.cfg, deps)
	if err != nil {
		return fail("server: %v", err)
	}
	if *offline {
		// -offline is not a mode of its own: a live label from bench.yaml is
		// served as recorded (offlineMode), so say what offline changes.
		a.logger.Info("bench UI and API on http://%s (mode %s, offline: no gateway, no launching)", a.cfg.Server.Listen, a.cfg.Server.Mode)
	} else {
		a.logger.Info("bench UI and API on http://%s (mode %s)", a.cfg.Server.Listen, a.cfg.Server.Mode)
	}
	if err := srv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
		return fail("serve: %v", err)
	}
	return exitOK
}

// orgDeps builds server.Deps over one org root: a's run store, its project
// root (a.cfg.Root: suites, agents, policies) and its registry. Tenancy area
// (P3): local mode calls it once with the app of the configured runs root;
// hosted mode calls it per org with that org's app (servetenancy.go), so an
// org's handlers are built over that org's folders and nothing else. The
// launcher, worker mode and the schedule daemon are wired by the caller.
func orgDeps(ctx context.Context, a *app) (server.Deps, *store.Index, error) {
	ix := store.NewIndex(a.store)
	if err := ix.Rebuild(ctx); err != nil {
		return server.Deps{}, nil, fmt.Errorf("index runs: %w", err)
	}
	deps := server.Deps{
		Index:    indexAdapter{ix: ix, root: a.store.Root()},
		Analyzer: analyzerAdapter{a: a, ix: ix},
		Catalog:  &catalogSource{a: a},
		Auditor:  auditor{},
		Doctor:   a.doctor,
		Releases: a.serveReleases(), // Tenancy area: a hosted org's own release log (servetenancy.go)
		Suites:   serveSuites{root: a.cfg.Path(a.cfg.BenchmarksRoot), cfg: a.cfg},
		Agents:   serveAgents{root: a.cfg.Root},
		UI:       builtUI(),
		Logger:   a.logger,
		Version:  benchVersion,
	}
	deps.Reviews = reviewSource{st: a.store} // Human review (review.go)
	// Forecast and matrix area: estimate and matrix routes (estimate.go, matrix.go).
	deps.Forecast = serveForecast{a: a}
	deps.Migration, deps.CostExplain = serveW6{a: a}, serveW6{a: a} // W6 migration report and cost explanation (migrate.go, costexplain.go)
	// Index and trends area: trends over this root's results index; the database index when a.results is set (servetenancy.go).
	deps.Trends = trendSource{ix: a.resultsSource(), logger: a.logger, manifests: a.store.LoadManifest}
	// Suite health area: GET /suites/{id}/health reads the run store (suitehealth.go).
	deps.Health = serveHealthSource(a.cfg)
	return deps, ix, nil
}

// builtUI returns the embedded UI, or nil when it has not been built.
func builtUI() fs.FS {
	sub, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// indexAdapter exposes the store index through the handlers' read interface.
type indexAdapter struct {
	ix   *store.Index
	root string // run store root, for the analysis folders
}

// LatestAnalysis returns the newest analysis id of an experiment from its
// analysis/ folder, so the verdict page survives a server restart.
func (x indexAdapter) LatestAnalysis(expID string) (string, bool) {
	entries, err := os.ReadDir(filepath.Join(x.root, expID, "analysis"))
	if err != nil {
		return "", false
	}
	var best string
	var bestAt time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestAt) {
			best, bestAt = e.Name(), info.ModTime()
		}
	}
	return best, best != ""
}

func view(e store.RunEntry) handlers.RunView {
	return handlers.RunView{Slot: e.Slot, Run: e.Run, Sealed: e.Sealed, SealHash: e.SealHash}
}

func views(es []store.RunEntry) []handlers.RunView {
	out := make([]handlers.RunView, 0, len(es))
	for _, e := range es {
		out = append(out, view(e))
	}
	return out
}

func (x indexAdapter) Experiments() []string { return x.ix.Experiments() }

func (x indexAdapter) Manifest(expID string) (*schemas.Manifest, bool) { return x.ix.Manifest(expID) }

func (x indexAdapter) Runs(expID string) []handlers.RunView {
	es, _ := x.ix.Runs(expID)
	return views(es)
}

func (x indexAdapter) Run(runID string) (*handlers.RunView, bool) {
	e, ok := x.ix.Run(runID)
	if !ok {
		return nil, false
	}
	v := view(e)
	return &v, true
}

func (x indexAdapter) Pair(pairID string) []handlers.RunView {
	es, _ := x.ix.Pair(pairID)
	return views(es)
}

func (x indexAdapter) RunFile(runID, name string) ([]byte, error) { return x.ix.RunFile(runID, name) }

// RunArtifact implements handlers.ArtifactReader (store/runartifact.go).
func (x indexAdapter) RunArtifact(runID, id, name string) ([]byte, error) {
	return x.ix.RunArtifact(runID, id, name)
}

// RunVisual implements handlers.VisualReader (X1 vision grading, store/runvisual.go).
func (x indexAdapter) RunVisual(runID, name string) ([]byte, error) {
	return x.ix.RunVisual(runID, name)
}

// RunVoiceFile implements handlers.VoiceReader (X8 voice agents, store/runvoice.go).
func (x indexAdapter) RunVoiceFile(runID, service, name string) ([]byte, error) {
	return x.ix.RunVoiceFile(runID, service, name)
}

// RunBlame implements handlers.BlameReader: the run's blame.json child
// artifact (attribution/artifact.go), or nil when none was written.
func (x indexAdapter) RunBlame(expID, runID, sealHash string) (*attribution.Blame, error) {
	return attribution.Read(x.root, expID, runID, sealHash)
}

// VerifySeal implements handlers.SealVerifier with the index's cached seal
// check, so the run detail and the leaderboard report a changed run folder.
func (x indexAdapter) VerifySeal(ctx context.Context, runID string) error {
	return x.ix.VerifyRun(ctx, runID)
}

// analyzerAdapter analyzes on demand and refreshes the index afterwards.
type analyzerAdapter struct {
	a  *app
	ix *store.Index
}

func (x analyzerAdapter) Analyze(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, error) {
	an, _, err := x.a.analyze(ctx, expID, gradeSource)
	if err != nil {
		return nil, err
	}
	_ = x.ix.Rebuild(ctx)
	return an, nil
}

// auditor wraps the verdict package's audit functions.
type auditor struct{}

func (auditor) AuditCSV(_ context.Context, csv []byte) (*schemas.Verdict, error) {
	rows, err := verdict.ParseAuditCSV(strings.NewReader(string(csv)))
	if err != nil {
		return nil, err
	}
	return verdict.Audit(rows, 0.05)
}

func (auditor) Audit(_ context.Context, req *schemas.AuditRequest) (*schemas.Verdict, error) {
	if req == nil {
		return nil, errors.New("empty audit request")
	}
	rows := make([]verdict.AuditRow, 0, len(req.Rows))
	for _, r := range req.Rows {
		rows = append(rows, verdict.AuditRow{Task: r.Task, APass: r.APass, AN: r.AN, BPass: r.BPass, BN: r.BN})
	}
	alpha := req.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	return verdict.Audit(rows, alpha)
}

// catalogSource builds the catalog once from the registry, the suites and the
// agent configs on disk.
type catalogSource struct {
	a    *app
	once sync.Once
	cat  *schemas.Catalog
}

func (c *catalogSource) Catalog() *schemas.Catalog {
	c.once.Do(func() { c.cat = c.build() })
	return c.cat
}

func (c *catalogSource) build() *schemas.Catalog {
	a := c.a
	cat := &schemas.Catalog{Mode: a.cfg.Server.Mode, Parts: engine.PartVersions(a.reg)}
	root := a.cfg.Path(a.cfg.BenchmarksRoot)
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := loadSuite(context.Background(), root, e.Name())
		if err != nil {
			continue
		}
		cat.Suites = append(cat.Suites, schemas.CatalogSuite{
			ID: s.ID, Version: s.Version, Tasks: s.TaskIDs, Probes: s.ProbeIDs, LockHash: s.LockHash,
		})
		for _, p := range s.Probes {
			cat.Probes = append(cat.Probes, *p)
		}
	}
	files, _ := filepath.Glob(filepath.Join(a.cfg.Root, "agents", "*.yaml"))
	sort.Strings(files)
	for _, f := range files {
		var cfg schemas.AgentConfig
		if err := suite.DecodeYAMLFile(f, f, suite.SchemaAgent, &cfg); err != nil {
			continue
		}
		rel, _ := filepath.Rel(a.cfg.Root, f)
		cat.Agents = append(cat.Agents, schemas.CatalogAgent{ID: cfg.ID, Path: rel, Adapter: cfg.Adapter, Model: cfg.Model, Parser: cfg.Parser,
			AgentVersion: cfg.AgentVersion, ImageDigest: cfg.ImageDigest, Capabilities: cfg.Capabilities})
	}
	for _, m := range a.reg.Metrics {
		cat.Metrics = append(cat.Metrics, m.Descriptor())
	}
	baseline := map[string]bool{}
	for _, n := range gates.BaselineNames() {
		baseline[n] = true
	}
	for _, g := range a.reg.Gates {
		cat.Gates = append(cat.Gates, schemas.CatalogGate{Name: g.GetName(), Version: g.GetVersion(), Effect: g.Effect(), Baseline: baseline[g.GetName()]})
	}
	return cat
}

// Jobs area: the launcher (experiments as experiment.run jobs, live events,
// cancel and resume) is in servejobs.go.

// offlineMode is the mode label a server serves: an offline server replays
// recorded runs, so a configured live mode becomes recorded.
func offlineMode(configured schemas.Mode, offline bool) schemas.Mode {
	if offline && configured == schemas.ModeLive {
		return schemas.ModeRecorded
	}
	return configured
}
