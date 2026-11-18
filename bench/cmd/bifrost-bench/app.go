package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/probes"
	"github.com/maximhq/bifrost/bench/reporters"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/verdict"
)

// defaultConfig is the config path used when -config is not given.
const defaultConfig = "deploy/bench.yaml"

// app is everything a command needs: config, logger, the assembled registry
// and the store. This is the only place registries are assembled.
type app struct {
	cfg       *lib.Config
	logger    schemas.Logger
	docker    utils.Docker
	reg       *schemas.Registry
	store     *store.FSStore
	upstreams engine.UpstreamChecker // the fault proxy's upstream check; nil without the gateway
	prices    *evidence.PriceTable   // the price snapshot new runs are costed with
}

// appOptions says which live services a command needs.
type appOptions struct {
	gateway bool // build the Bifrost gateway client and the fault proxy client
	debug   bool
}

// newApp loads the config and assembles the registry.
func newApp(ctx context.Context, configPath string, opts appOptions) (*app, error) {
	cfg, err := lib.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	logger := utils.NewStdLogger(os.Stderr, opts.debug)
	a := &app{cfg: cfg, logger: logger, docker: utils.Docker{}}

	st, err := store.NewFSStore(cfg.Path(cfg.RunsRoot), logger)
	if err != nil {
		return nil, fmt.Errorf("open run store: %w", err)
	}
	a.store = st

	var fault schemas.FaultController
	var gw schemas.Gateway
	if opts.gateway {
		fp := faultproxy.NewClient(cfg.FaultProxy.ControlURL, faultproxy.WithToken(os.Getenv("BENCH_FAULTPROXY_TOKEN")))
		fault = fp
		a.upstreams = proxyUpstreams{fp}
		gw, err = newGateway(ctx, cfg, a.docker, logger)
		if err != nil {
			return nil, err
		}
	}

	prices, err := loadPrices(cfg.Path(cfg.Pricing), logger)
	if err != nil {
		return nil, fmt.Errorf("load prices: %w", err)
	}
	a.prices = prices
	parsers := adapters.BuiltinParsers()
	adapterFaults := environments.NewAdapterFaultController(environments.DockerWorkspaceConfig{Docker: a.docker, Logger: logger, HelperImage: cfg.Images.Helper, ToolProxyImage: cfg.Images.ToolProxy})
	helper := cfg.Images.Helper
	a.reg = &schemas.Registry{
		Loaders: suite.Builtin(cfg.Path(cfg.BenchmarksRoot)),
		Environments: environments.Builtin(environments.DockerWorkspaceConfig{
			EvalNetwork: cfg.Networks.Eval, HelperImage: helper, Logger: logger, Docker: a.docker,
			ToolProxyImage: cfg.Images.ToolProxy,
			// Agent steps area: trusted step observers, on unless switched off.
			ProcessWatch: cfg.Observers.ProcessOn(), WorkspaceWatch: cfg.Observers.WorkspaceOn(), WatchIntervalMS: cfg.Observers.IntervalMS,
			EventsCollector:   cfg.Events.CollectorOn(),                                                // Agent events area
			RemoteToolsListen: cfg.RemoteTools.Listen, RemoteToolsPublicURL: cfg.RemoteTools.PublicURL, // Remote coverage
			ServiceGateway: environments.ServiceGatewayConfig{Gateway: gw, AgentURL: cfg.Gateway.AgentURL, Fault: fault, Price: graders.PricesFrom(prices.Lookup), PriceRef: prices.Ref()}, // W3 customer support: services' own keys
		}),
		Adapters: adapters.Builtin(adapters.CommandConfig{
			GatewayAgentURL: cfg.Gateway.AgentURL, EvalNetwork: cfg.Networks.Eval, BenchRoot: cfg.Root,
			GatewayPublicURL: cfg.Gateway.PublicURL, // Remote coverage: the gateway as a remote agent reaches it
		}),
		Parsers:     parsers,
		ProbeLayers: probes.Builtin(probes.Deps{Docker: a.docker, HelperImage: helper, Fault: fault, AdapterFault: adapterFaults, Logger: logger}),
		Evidence: evidence.Builtin(evidence.Deps{
			Gateway: gw, Fault: fault, Parsers: parsers, Docker: a.docker, HelperImage: helper, Prices: prices, Logger: logger,
		}),
		Graders:       append([]schemas.Grader{graders.NewJudge(graders.JudgeConfig{Gateway: gw, URL: cfg.Gateway.URL, Price: graders.PricesFrom(prices.Lookup), PriceRef: prices.Ref(), Logger: logger})}, graders.Builtin(graders.HiddenTestsConfig{Image: helper, Logger: logger, Docker: a.docker})...), // Memory and answers: the wired judge, ahead of Builtin's unwired one
		Gates:         gates.Builtin(),
		Metrics:       metrics.Builtin(),
		Policies:      verdict.Builtin(),
		Reporters:     reporters.Builtin(),
		Gateway:       gw,
		Fault:         fault,
		AdapterFaults: adapterFaults,
		Store:         st,
	}
	if fault == nil {
		a.reg.Fault = nil // a typed nil would look non-nil to the engine
	}
	a.reg.Graders = withGrader(a.reg.Graders, graders.NewVisual(graders.VisualConfig{Inner: graders.HiddenTestsConfig{Image: helper, Logger: logger, Docker: a.docker}, Judge: graders.JudgeConfig{Gateway: gw, URL: cfg.Gateway.URL, Price: graders.PricesFrom(prices.Lookup), PriceRef: prices.Ref(), Logger: logger}, Logger: logger})) // X1 vision grading: the wired visual grader replaces Builtin's unwired one (visual.go)
	return a, nil
}

// pricingDefault is the project path of the embedded price snapshot.
const pricingDefault = "deploy/pricing-snapshot.json"

// loadPrices reads the price snapshot bench.yaml names. When the file does
// not exist (a project folder copied without it, or a config that names a
// path that was never written) it falls back to the snapshot built into the
// binary, the one `init` writes, with a warning, instead of refusing every
// command. A file that exists but does not parse is still an error: a
// broken snapshot must never be replaced silently. Cost rows record the
// snapshot's id and sha256 either way.
func loadPrices(path string, logger schemas.Logger) (*evidence.PriceTable, error) {
	prices, err := evidence.LoadPrices(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return prices, err
	}
	raw, derr := scaffold.ProjectDefault(pricingDefault)
	if derr != nil {
		return nil, fmt.Errorf("%w (and no embedded default: %v)", err, derr)
	}
	prices, derr = evidence.ParsePrices(raw, "embedded default "+pricingDefault)
	if derr != nil {
		return nil, fmt.Errorf("%w (and the embedded default does not load: %v)", err, derr)
	}
	logger.Warn("price snapshot not found; using the embedded default (restore the file, or point pricing in deploy/bench.yaml at a snapshot)",
		"path", path, "snapshot", prices.Ref())
	return prices, nil
}

// newPlanner returns the planner every command uses, with the fault proxy's
// upstream check wired when the gateway is, so a live experiment is never
// frozen while its provider cannot be reached.
func (a *app) newPlanner() *engine.Planner {
	var opts []engine.PlannerOption
	if a.upstreams != nil {
		opts = append(opts, engine.WithUpstreamChecker(a.upstreams))
	}
	opts = append(opts, engine.WithModelPrices(a.priced)) // Forecast and matrix area: refuse unpriced matrix models (estimate.go)
	// Suite health area: -include-quarantined (suitehealth.go).
	opts = append(opts, engine.WithIncludeQuarantined(includeQuarantined))
	return engine.NewPlanner(a.reg, a.cfg.Root, a.logger, opts...)
}

// proxyUpstreams adapts the fault proxy client to engine.UpstreamChecker, so
// the engine never imports faultproxy. An older proxy's answer becomes
// engine.ErrUpstreamCheckUnsupported.
type proxyUpstreams struct {
	c *faultproxy.Client
}

// CheckUpstreams implements engine.UpstreamChecker.
func (u proxyUpstreams) CheckUpstreams(ctx context.Context) ([]engine.UpstreamStatus, error) {
	rep, err := u.c.CheckUpstreams(ctx)
	if errors.Is(err, faultproxy.ErrUpstreamCheckUnsupported) {
		return nil, fmt.Errorf("%w: %w", engine.ErrUpstreamCheckUnsupported, err)
	}
	if err != nil {
		return nil, err
	}
	out := make([]engine.UpstreamStatus, 0, len(rep.Upstreams))
	for _, c := range rep.Upstreams {
		out = append(out, engine.UpstreamStatus{Provider: c.Provider, Host: c.Host, OK: c.OK, Skipped: c.Skipped, Error: c.Error})
	}
	return out, nil
}

// loadExperiment reads and strictly decodes an experiment file.
func loadExperiment(path string) (*schemas.Experiment, error) {
	var exp schemas.Experiment
	if err := suite.DecodeYAMLFile(path, path, suite.SchemaExperiment, &exp); err != nil {
		return nil, err
	}
	return &exp, nil
}

// analyze runs the experiment's verdict policy over its sealed runs, writes
// the analysis and its markdown report, and returns both. A grade source that
// recomputed gates adds a Gate source section to the report.
func (a *app) analyze(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, []byte, error) {
	return a.analyzeWith(ctx, expID, gradeSource, "")
}

// analyzeWith is analyze with a release policy file that overrides the one
// the experiment froze, for `check -policy`. The policy result is part of the
// analysis, so the report, the JSON and the verdict route all carry it.
//
// Every run SealedRuns returns has been checked against its seal, and a run
// that does not verify makes the verdict INVALID with a reason naming it and
// the file. The experiment's seals.log chain is verified here once per
// analysis: a broken chain (a line edited, a sealed run deleted, a seal
// missing from the log) makes the verdict INVALID too. The release policy is
// handed the runs with every missing slot added as an unknown, as the verdict
// sees them.
func (a *app) analyzeWith(ctx context.Context, expID, gradeSource, policyOverride string) (*schemas.Analysis, []byte, error) {
	m, err := a.store.LoadManifest(ctx, expID)
	if err != nil {
		return nil, nil, err
	}
	if gradeSource == "" {
		gradeSource = schemas.GradeSourceOriginal
	}
	runs, err := a.store.SealedRuns(ctx, expID, gradeSource)
	if err != nil {
		return nil, nil, fmt.Errorf("load sealed runs: %w", err)
	}
	policyName := m.Experiment.Verdict.Policy
	if policyName == "" {
		policyName = "paired_signflip"
	}
	var policy schemas.VerdictPolicy
	for _, p := range a.reg.Policies {
		if p.GetName() == policyName {
			policy = p
		}
	}
	if policy == nil {
		return nil, nil, fmt.Errorf("verdict policy %q is not registered", policyName)
	}
	an, err := policy.Decide(ctx, m, runs)
	if err != nil {
		return nil, nil, fmt.Errorf("decide: %w", err)
	}
	if err := a.store.VerifyChain(expID); err != nil {
		if !errors.Is(err, schemas.ErrSealMismatch) {
			return nil, nil, fmt.Errorf("verify seal chain: %w", err)
		}
		verdict.Invalidate(an, "evidence: seals.log chain does not verify: "+err.Error())
	}
	a.judgeCalibration(ctx, m, an, runs, policyOverride) // W8 judge calibration: judge_calibration instrument of judged experiments (judge.go)
	an.AnalysisID = utils.NewID("a", 8)
	an.Mode = a.cfg.Server.Mode
	// A declared smoke experiment is never a finding, whatever the server mode.
	if m.Experiment.Smoke {
		an.Mode = schemas.ModeSynthetic
	}
	an.GradeSource = gradeSource
	if an.Policy, err = a.releasePolicy(m, an, verdict.CompleteSlots(m, runs), policyOverride); err != nil {
		return nil, nil, err
	}
	var md strings.Builder
	for _, r := range a.reg.Reporters {
		if r.GetName() == "markdown" {
			if err := r.Write(ctx, an, m, &md); err != nil {
				return nil, nil, fmt.Errorf("write report: %w", err)
			}
		}
	}
	sec, err := a.gateSourceSection(ctx, m, gradeSource, runs)
	if err != nil {
		return nil, nil, fmt.Errorf("gate source: %w", err)
	}
	report := insertSection(md.String(), sec)
	report = a.reviewSummary(ctx, an, runs, report) // human review: counts and report section (review.go)
	report = a.unreviewedSection(ctx, m, report)    // First workflow: "N of M tasks have unreviewed instructions" (compare.go)
	report = a.incidentSection(runs, report)        // Failure inbox area: regressions of production incidents (inbox.go)
	report = adaptiveSection(an, report)            // W7 adaptive trials and tiers: trials used, runs saved, stop reason, more evidence (adaptivereport.go)
	// W8 judge calibration: report section (judge.go).
	report = a.judgeCalibrationSection(an, report)
	if err := a.store.WriteAnalysis(ctx, an, map[string][]byte{"report.md": []byte(report)}); err != nil {
		return nil, nil, fmt.Errorf("save analysis: %w", err)
	}
	return an, []byte(report), nil
}

// releasePolicy evaluates the release policy over the analysis: the override
// file when one is given (a path from the command line), otherwise the policy
// the experiment froze (relative to the bench root), otherwise none. An
// override that cannot be loaded is an error. A frozen policy that cannot be
// loaded gives a blocking result instead, so a deleted or broken policy file
// can never let a release through, and the verdict page still renders.
func (a *app) releasePolicy(m *schemas.Manifest, an *schemas.Analysis, runs []schemas.SealedRun, override string) (*schemas.PolicyResult, error) {
	if override != "" {
		p, raw, err := policy.Load(override)
		if err != nil {
			return nil, err
		}
		return policy.Evaluate(p, policy.Hash(raw), m, an, runs), nil
	}
	if m.Experiment.Policy == "" {
		return nil, nil
	}
	p, raw, err := policy.Load(a.cfg.Path(m.Experiment.Policy))
	if err != nil {
		a.logger.Warn("release policy could not be loaded; the release is blocked", "experiment", m.ExpID, "policy", m.Experiment.Policy, "error", err.Error())
		return policy.LoadFailure(m.Experiment.Policy, m.PolicyHash, err), nil
	}
	return policy.Evaluate(p, policy.Hash(raw), m, an, runs), nil
}

// validatePolicy loads the policy an experiment names before anything is
// planned, so a schema error is reported before a run starts, not after.
func (a *app) validatePolicy(exp *schemas.Experiment) error {
	if exp.Policy == "" {
		return nil
	}
	if _, _, err := policy.Load(a.cfg.Path(exp.Policy)); err != nil {
		return err
	}
	return nil
}

// benchRelative turns a path given on the command line into one the planner
// resolves against the bench root.
func (a *app) benchRelative(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if rel, err := filepath.Rel(a.cfg.Root, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return abs
}
