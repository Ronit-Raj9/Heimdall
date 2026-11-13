package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/probes"
	"github.com/maximhq/bifrost/bench/reporters"
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
	cfg    *lib.Config
	logger schemas.Logger
	docker utils.Docker
	reg    *schemas.Registry
	store  *store.FSStore
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
		fault = faultproxy.NewClient(cfg.FaultProxy.ControlURL, faultproxy.WithToken(os.Getenv("BENCH_FAULTPROXY_TOKEN")))
		gw, err = newGateway(ctx, cfg, a.docker, logger)
		if err != nil {
			return nil, err
		}
	}

	prices, err := evidence.LoadPrices(cfg.Path(cfg.Pricing))
	if err != nil {
		return nil, fmt.Errorf("load prices: %w", err)
	}
	parsers := adapters.BuiltinParsers()
	helper := cfg.Images.Helper
	a.reg = &schemas.Registry{
		Loaders: suite.Builtin(cfg.Path(cfg.BenchmarksRoot)),
		Environments: environments.Builtin(environments.DockerWorkspaceConfig{
			EvalNetwork: cfg.Networks.Eval, HelperImage: helper, Logger: logger, Docker: a.docker,
		}),
		Adapters: adapters.Builtin(adapters.CommandConfig{
			GatewayAgentURL: cfg.Gateway.AgentURL, EvalNetwork: cfg.Networks.Eval, BenchRoot: cfg.Root,
		}),
		Parsers:     parsers,
		ProbeLayers: probes.Builtin(probes.Deps{Docker: a.docker, HelperImage: helper, Fault: fault, Logger: logger}),
		Evidence: evidence.Builtin(evidence.Deps{
			Gateway: gw, Fault: fault, Parsers: parsers, Docker: a.docker, HelperImage: helper, Prices: prices, Logger: logger,
		}),
		Graders:   graders.Builtin(graders.HiddenTestsConfig{Image: helper, Logger: logger, Docker: a.docker}),
		Gates:     gates.Builtin(),
		Metrics:   metrics.Builtin(),
		Policies:  verdict.Builtin(),
		Reporters: reporters.Builtin(),
		Gateway:   gw,
		Fault:     fault,
		Store:     st,
	}
	if fault == nil {
		a.reg.Fault = nil // a typed nil would look non-nil to the engine
	}
	return a, nil
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
// the analysis and its markdown report, and returns both.
func (a *app) analyze(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, []byte, error) {
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
	an.AnalysisID = utils.NewID("a", 8)
	an.Mode = a.cfg.Server.Mode
	an.GradeSource = gradeSource
	var md strings.Builder
	for _, r := range a.reg.Reporters {
		if r.GetName() == "markdown" {
			if err := r.Write(ctx, an, m, &md); err != nil {
				return nil, nil, fmt.Errorf("write report: %w", err)
			}
		}
	}
	if err := a.store.WriteAnalysis(ctx, an, map[string][]byte{"report.md": []byte(md.String())}); err != nil {
		return nil, nil, fmt.Errorf("save analysis: %w", err)
	}
	return an, []byte(md.String()), nil
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
