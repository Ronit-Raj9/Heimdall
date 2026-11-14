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
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{gateway: !*offline, debug: *debug})
	if err != nil {
		return fail("%v", err)
	}
	ix := store.NewIndex(a.store)
	if err := ix.Rebuild(ctx); err != nil {
		return fail("index runs: %v", err)
	}
	deps := server.Deps{
		Index:    indexAdapter{ix: ix, root: a.store.Root()},
		Analyzer: analyzerAdapter{a: a, ix: ix},
		Catalog:  &catalogSource{a: a},
		Auditor:  auditor{},
		Doctor:   a.doctor,
		Releases: serveReleaseLog(a),
		Suites:   serveSuites{root: a.cfg.Path(a.cfg.BenchmarksRoot)},
		Agents:   serveAgents{root: a.cfg.Root},
		UI:       builtUI(),
		Logger:   a.logger,
		Version:  benchVersion,
	}
	if !*offline {
		deps.Launcher = newLauncher(a, ix)
	}
	srv, err := server.New(a.cfg, deps)
	if err != nil {
		return fail("server: %v", err)
	}
	if *offline {
		// -offline is not a mode: the mode label (live, recorded, synthetic)
		// still comes from bench.yaml, so say what offline changes.
		a.logger.Info("bench UI and API on http://%s (mode %s, offline: no gateway, no launching)", a.cfg.Server.Listen, a.cfg.Server.Mode)
	} else {
		a.logger.Info("bench UI and API on http://%s (mode %s)", a.cfg.Server.Listen, a.cfg.Server.Mode)
	}
	if err := srv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
		return fail("serve: %v", err)
	}
	return exitOK
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
		cat.Agents = append(cat.Agents, schemas.CatalogAgent{ID: cfg.ID, Path: rel, Adapter: cfg.Adapter, Model: cfg.Model, Parser: cfg.Parser})
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

// launcher plans, freezes and runs experiments for the API and fans live
// events out to subscribers.
type launcher struct {
	a  *app
	ix *store.Index

	mu     sync.Mutex
	active map[string]*liveRun
}

// liveRun is one running experiment's event fan-out.
type liveRun struct {
	mu   sync.Mutex
	seq  int
	subs map[chan schemas.LiveEvent]bool
	done bool
}

func newLauncher(a *app, ix *store.Index) *launcher {
	return &launcher{a: a, ix: ix, active: map[string]*liveRun{}}
}

func (l *launcher) Plan(ctx context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	return l.a.newPlanner().Plan(ctx, exp)
}

func (l *launcher) Create(ctx context.Context, m *schemas.Manifest) error {
	if err := l.a.store.CreateExperiment(ctx, m); err != nil {
		return err
	}
	return l.ix.Rebuild(ctx)
}

// Active reports whether an experiment is running in this process.
func (l *launcher) Active(expID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.active[expID]
	return ok && !r.done
}

func (l *launcher) Start(ctx context.Context, expID string) error {
	m, err := l.a.store.LoadManifest(ctx, expID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	if r, ok := l.active[expID]; ok && !r.done {
		l.mu.Unlock()
		return fmt.Errorf("experiment %s is already running", expID)
	}
	run := &liveRun{subs: map[chan schemas.LiveEvent]bool{}}
	l.active[expID] = run
	l.mu.Unlock()

	go l.execute(m, run)
	return nil
}

// execute runs the experiment detached from any request, analyzes it, and
// closes every subscriber.
func (l *launcher) execute(m *schemas.Manifest, run *liveRun) {
	ctx := context.Background()
	events := make(chan engine.Event, 256)
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for ev := range events {
			run.publish(schemas.LiveEvent{Kind: ev.Kind, SlotID: ev.SlotID, RunID: ev.RunID, Stage: ev.Stage, Outcome: ev.Outcome, Message: ev.Message, At: ev.At})
			if ev.Kind == engine.EventRunSealed {
				_ = l.ix.Rebuild(ctx)
			}
		}
	}()
	janitor := engine.NewJanitor(l.a.reg.Gateway, l.a.docker, l.a.logger)
	sched := engine.NewScheduler(l.a.reg, l.a.cfg.Root, l.a.logger, janitor)
	// Create already froze the experiment, so the scheduler runs in resume mode.
	_, err := sched.Run(ctx, m, engine.RunOptions{Resume: true, Events: events})
	close(events)
	<-forwarded
	if err != nil {
		run.publish(schemas.LiveEvent{Kind: "error", Message: err.Error(), At: time.Now()})
	}
	if an, _, aerr := l.a.analyze(ctx, m.ExpID, ""); aerr != nil {
		run.publish(schemas.LiveEvent{Kind: "error", Message: "analyze: " + aerr.Error(), At: time.Now()})
	} else {
		run.publish(schemas.LiveEvent{Kind: "experiment_done", Message: string(an.Verdict.Decision), At: time.Now()})
	}
	_ = l.ix.Rebuild(ctx)
	run.finish()
}

func (l *launcher) Events(expID string) (<-chan schemas.LiveEvent, func()) {
	l.mu.Lock()
	run, ok := l.active[expID]
	l.mu.Unlock()
	ch := make(chan schemas.LiveEvent, 64)
	if !ok {
		close(ch)
		return ch, func() {}
	}
	return run.subscribe(ch)
}

func (r *liveRun) publish(ev schemas.LiveEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	ev.Seq = r.seq
	for ch := range r.subs {
		select {
		case ch <- ev:
		default: // a slow subscriber misses events; the poll endpoint can catch up
		}
	}
}

func (r *liveRun) subscribe(ch chan schemas.LiveEvent) (<-chan schemas.LiveEvent, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		close(ch)
		return ch, func() {}
	}
	r.subs[ch] = true
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.subs[ch] {
				delete(r.subs, ch)
				close(ch)
			}
		})
	}
}

func (r *liveRun) finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done = true
	for ch := range r.subs {
		close(ch)
		delete(r.subs, ch)
	}
}
