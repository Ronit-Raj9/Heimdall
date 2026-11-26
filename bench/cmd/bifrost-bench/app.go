package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/maximhq/bifrost/bench/compat"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/gateways/bifrost"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/probes"
	"github.com/maximhq/bifrost/bench/reporters"
	"github.com/maximhq/bifrost/bench/resultsindex"
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
	upstreams engine.UpstreamChecker             // the fault proxy's upstream check; nil without the gateway
	prices    *evidence.PriceTable               // the price snapshot new runs are costed with
	dockerCfg environments.DockerWorkspaceConfig // the run environment's wiring (service gateway included); agent check reuses it

	// Tenancy area (P3): set only on a hosted org's app (servetenancy.go).
	orgID        string                          // the org this app serves; empty in local mode
	releasesRoot string                          // the org's release log; empty uses release.DefaultRoot
	planGuard    func(*schemas.Experiment) error // confines experiment paths to the org's project
	planConfine  string                          // agent config files (notes) must resolve inside it (hosted)
	declaredGPUs func() []schemas.GPUInfo        // Z05: GPUs the org's workers declare (hosted: gpuplan_hosted.go)
	deferGateway bool                            // the gateway preflight is the runners' (hosted: deferred_to_runner)
	results      resultsindex.Source             // the org's database index (db.OrgIndex); nil uses the JSONL index
	library      string                          // the server's built-in benchmark library (suite.LibraryDir); "" for none

	// defaultPolicy is evaluated only when the experiment froze no policy
	// (`check -default-policy`, the CI wrapper's implicit default). Unlike an
	// override it never replaces a frozen policy or skips its hash check
	// (QC2-22-02).
	defaultPolicy string
}

// appOptions says which live services a command needs.
type appOptions struct {
	gateway bool // build the Bifrost gateway client and the fault proxy client
	debug   bool
	// Shared runner (runnershared.go): one app per served org, with that
	// org's docker policy and its own eval network.
	docker      *utils.Docker // nil: the plain docker CLI
	evalNetwork string        // "": networks.eval from the config
	// sharedRunner turns the judge cache off: the shared worker serves
	// many organizations from one config root (judgecache.go).
	sharedRunner bool
}

// newApp loads the config and assembles the registry.
func newApp(ctx context.Context, configPath string, opts appOptions) (*app, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}
	logger := utils.NewStdLogger(os.Stderr, opts.debug)
	if opts.evalNetwork != "" {
		cfg.Networks.Eval = opts.evalNetwork
	}
	a := &app{cfg: cfg, logger: logger, docker: utils.Docker{}, library: suite.LibraryDir()} // built-in library (suite/library.go)
	if opts.docker != nil {
		a.docker = *opts.docker
	}

	st, err := store.NewFSStore(cfg.Path(cfg.RunsRoot), logger)
	if err != nil {
		return nil, fmt.Errorf("open run store: %w", err)
	}
	a.store = st
	utils.SetProject(utils.ProjectID(cfg.Path(cfg.RunsRoot))) // 5-A-04: label per-run docker objects with their project, for the janitor

	var fault schemas.FaultController
	var gw schemas.Gateway
	if opts.gateway {
		fp := faultproxy.NewClient(cfg.FaultProxy.ControlURL, faultproxy.WithToken(os.Getenv("BENCH_FAULTPROXY_TOKEN")))
		// C04: HEIMDALL_FAULT_MODE=plugin hands model faults to the heimdall
		// Bifrost plugin for agents that send its headers; default proxy.
		if fault, err = bifrost.FaultControllerFromEnv(fp); err != nil {
			return nil, err
		}
		a.upstreams = proxyUpstreams{fp}
		gw, err = newGateway(ctx, cfg, a.docker, logger, fp) // F1: the plan-writing client holds the slot claims
		if err != nil {
			return nil, err
		}
		if c, ok := gw.(interface{ Close(context.Context) error }); ok { // F1: release this process's slot claims at exit
			onExit(func() { _ = c.Close(context.Background()) })
		}
	}

	prices, err := loadPrices(cfg.Path(cfg.Pricing), logger)
	if err != nil {
		return nil, fmt.Errorf("load prices: %w", err)
	}
	a.prices = prices
	handlers.SetPriceList(prices.Lookup) // F1.price: usage, report card and the default model's hint read the same price list
	parsers := adapters.BuiltinParsers()
	adapterFaults := environments.NewAdapterFaultController(environments.DockerWorkspaceConfig{Docker: a.docker, Logger: logger, HelperImage: cfg.Images.Helper, ToolProxyImage: cfg.Images.ToolProxy})
	helper := cfg.Images.Helper
	dockerCfg := environments.DockerWorkspaceConfig{
		EvalNetwork: cfg.Networks.Eval, HelperImage: helper, Logger: logger, Docker: a.docker,
		ToolProxyImage: cfg.Images.ToolProxy,
		// Agent steps area: trusted step observers, on unless switched off.
		ProcessWatch: cfg.Observers.ProcessOn(), WorkspaceWatch: cfg.Observers.WorkspaceOn(), WatchIntervalMS: cfg.Observers.IntervalMS,
		EventsCollector:   cfg.Events.CollectorOn(),                                                // Agent events area
		RemoteToolsListen: cfg.RemoteTools.Listen, RemoteToolsPublicURL: cfg.RemoteTools.PublicURL, // Remote coverage
		RemoteToolsIngressNetwork: cfg.RemoteTools.IngressNetwork, GatewayPublicURL: cfg.Gateway.PublicURL, GatewayAgentURL: cfg.Gateway.AgentURL, // Y03: public ingress and reachability refusals
		ServiceGateway: environments.ServiceGatewayConfig{Gateway: gw, AgentURL: cfg.Gateway.AgentURL, Fault: fault, Price: graders.PricesFrom(prices.Lookup), PriceRef: prices.Ref()}, // W3 customer support: a service's own gateway key (servicegateway.go)
	}
	a.dockerCfg = dockerCfg
	// Text benchmarks of voice platform agents run through the pull
	// launcher, so they need an address runs reach this server at.
	compat.VoicePlatformText = cfg.PullRunURL() != ""
	a.reg = &schemas.Registry{
		Loaders:      suite.Builtin(cfg.Path(cfg.BenchmarksRoot)),
		Environments: []schemas.Environment{environments.NewDockerDriver(dockerCfg)}, // X3: the Docker driver, today's docker_workspace behind the driver seam (environments/driver.go)
		Adapters: adapters.Builtin(adapters.CommandConfig{
			GatewayAgentURL: cfg.Gateway.AgentURL, EvalNetwork: cfg.Networks.Eval, BenchRoot: cfg.Root,
			GatewayPublicURL: cfg.Gateway.PublicURL, // Remote coverage: the gateway as a remote agent reaches it
			PullURL:          cfg.PullRunURL(),      // Y11 pull-mode agents (adapters/pull.go)
		}),
		Parsers:     parsers,
		ProbeLayers: probes.Builtin(probes.Deps{Docker: a.docker, HelperImage: helper, Fault: fault, AdapterFault: adapterFaults, Logger: logger}),
		Evidence: evidence.Builtin(evidence.Deps{
			Gateway: gw, Fault: fault, Parsers: parsers, Docker: a.docker, HelperImage: helper, Prices: prices, Logger: logger,
		}),
		Graders:       append([]schemas.Grader{graders.NewJudge(graders.JudgeConfig{Gateway: gw, URL: cfg.Gateway.URL, Price: graders.PricesFrom(prices.Lookup), PriceRef: prices.Ref(), Logger: logger, Cache: judgeCache(cfg, opts)})}, graders.Builtin(graders.HiddenTestsConfig{Image: helper, Logger: logger, Docker: a.docker})...), // Memory and answers: the wired judge, ahead of Builtin's unwired one
		Gates:         gates.Builtin(),
		Metrics:       append(metrics.Builtin(), graders.ProcessMetrics()...), // X23: report-only trajectory metrics (graders/process.go)
		Policies:      verdict.Builtin(),
		Reporters:     reporters.Builtin(),
		Gateway:       gw,
		Fault:         fault,
		AdapterFaults: adapterFaults,
		Store:         st,
	}
	a.reg.Evidence = append(a.reg.Evidence, codescan.NewSource(graders.ScanInCleanRoom(graders.HiddenTestsConfig{Image: helper, Logger: logger, Docker: a.docker}))) // X4: code scan of added lines
	// X3: environment.driver kubernetes swaps in the Kubernetes driver (k8s.go).
	if err := a.useKubernetes(dockerCfg); err != nil {
		return nil, err
	}
	if fault == nil {
		a.reg.Fault = nil // a typed nil would look non-nil to the engine
	}
	a.reg.Graders = withGrader(a.reg.Graders, graders.NewVisual(graders.VisualConfig{Inner: graders.HiddenTestsConfig{Image: helper, Logger: logger, Docker: a.docker}, Judge: graders.JudgeConfig{Gateway: gw, URL: cfg.Gateway.URL, Price: graders.PricesFrom(prices.Lookup), PriceRef: prices.Ref(), Logger: logger, Cache: judgeCache(cfg, opts)}, Logger: logger})) // X1 vision grading: the wired visual grader replaces Builtin's unwired one (visual.go)
	a.reg.Graders = withGrader(a.reg.Graders, graders.NewChecks(graders.ChecksConfig{Inner: graders.HiddenTestsConfig{Image: helper, Logger: logger, Docker: a.docker}, Judge: graders.JudgeConfig{Gateway: gw, URL: cfg.Gateway.URL, Price: graders.PricesFrom(prices.Lookup), PriceRef: prices.Ref(), Logger: logger, Cache: judgeCache(cfg, opts)}, Logger: logger})) // declarative checks.yaml: the wired checks grader (graders/checks.go)
	a.reg.GPUs = gpuAllocator(ctx, cfg, logger)                                                                                                                                                                                                                                                                                                                          // X7: this machine's GPU pool, nil without a usable GPU (gpu.go)
	// G01: runs resolve org secrets from the key store; only local mode falls back to the environment (secrets_service.go).
	a.reg.Secrets = orgSecretSource{keys: sharedKeyStore(a), orgID: auth.LocalOrgID, envFallback: !cfg.Auth.IsHosted()}
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
	if a.planGuard != nil {
		opts = append(opts, engine.WithExperimentGuard(a.planGuard)) // Tenancy area: hosted org paths stay in its project (servetenancy.go)
	}
	if a.planConfine != "" {
		opts = append(opts, engine.WithFilesConfinedTo(a.planConfine)) // and so do the files the resolved agent configs name (4-B-14)
	}
	if a.library != "" {
		opts = append(opts, engine.WithAgentLibrary(a.library)) // built-in library: agents/<file> the project lacks (suite/library.go)
	}
	if a.deferGateway {
		opts = append(opts, engine.WithGatewayDeferredToRunner()) // Tenancy area: a hosted server has no gateway; runners preflight theirs (servetenancy.go)
	}
	if a.declaredGPUs != nil {
		opts = append(opts, engine.WithDeclaredGPUs(a.declaredGPUs)) // Z05: no GPU here; plan from the org's GPU workers (engine/gpu.go)
	}
	if a.reg.Secrets != nil {
		opts = append(opts, engine.WithSecretCheck(a.reg.Secrets)) // G01: a missing org secret is a plan error (engine/secrets.go)
	}
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
	return a.analyzeOpt(ctx, expID, gradeSource, policyOverride, false)
}

// analyzeView is analyze for a read route (the verdict, diagnostics and
// adaptive pages): when an identical analysis is already saved it is
// returned instead of writing another, so browsing a page never piles up
// analysis folders (audit 4-B-07).
func (a *app) analyzeView(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, []byte, error) {
	return a.analyzeOpt(ctx, expID, gradeSource, "", true)
}

func (a *app) analyzeOpt(ctx context.Context, expID, gradeSource, policyOverride string, reuse bool) (*schemas.Analysis, []byte, error) {
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
	an, err := a.releaseVerdict(ctx, m, runs, policyOverride)
	if err != nil {
		return nil, nil, err
	}
	an.AnalysisID = utils.NewID("a", 8)
	an.Mode = a.cfg.Server.Mode
	// A declared smoke or scripted_upstream experiment is never a finding,
	// whatever the server mode: its model answers were scripted (QC2-15-01).
	if m.Experiment.NoPaidTraffic() {
		an.Mode = schemas.ModeSynthetic
	}
	an.GradeSource = gradeSource
	if an.InputsDigest, err = utils.AnalysisInputsDigest(runs); err != nil {
		return nil, nil, fmt.Errorf("digest analysis inputs: %w", err)
	}
	// A one-setup score run has no candidate to release, so no release
	// policy applies: its checks would all target a setup B that does not
	// exist and block (N61). Stored analyses keep their old result.
	if an.Verdict.Decision != schemas.DecisionScore {
		if an.Policy, err = a.releasePolicy(m, an, verdict.DecisionRuns(m, runs), policyOverride); err != nil {
			return nil, nil, err
		}
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
	if reuse {
		if same := a.sameAnalysis(an, report); same != nil {
			return same, []byte(report), nil
		}
	}
	if err := a.store.WriteAnalysis(ctx, an, map[string][]byte{"report.md": []byte(report)}); err != nil {
		return nil, nil, fmt.Errorf("save analysis: %w", err)
	}
	return an, []byte(report), nil
}

// releaseVerdict is the release verdict of m over its counted runs, with no
// side effect: the verdict policy's decision over the counted runs plus every
// other sealed attempt (for the harness-level egress check, audit
// qc2-02-05), then the seal chain, the frozen manifest's hash and the judge
// calibration instrument. analyze saves it; migration reads its validity and
// caps, so the two surfaces can never disagree on whether the evidence
// decides (audits qc2-05-02, qc2-05-08).
func (a *app) releaseVerdict(ctx context.Context, m *schemas.Manifest, runs []schemas.SealedRun, policyOverride string) (*schemas.Analysis, error) {
	vp, err := engine.FindVerdictPolicy(a.reg, m.Experiment.Verdict.Policy)
	if err != nil {
		return nil, err
	}
	harness, err := a.store.HarnessAttempts(ctx, m.ExpID)
	if err != nil {
		return nil, fmt.Errorf("load harness attempts: %w", err)
	}
	an, err := vp.Decide(ctx, m, append(slices.Clip(runs), harness...))
	if err != nil {
		return nil, fmt.Errorf("decide: %w", err)
	}
	if err := a.store.VerifyChain(m.ExpID); err != nil {
		if !errors.Is(err, schemas.ErrSealMismatch) {
			return nil, fmt.Errorf("verify seal chain: %w", err)
		}
		verdict.Invalidate(an, "evidence: seals.log chain does not verify: "+err.Error())
	}
	// The manifest read here is the frozen one only when it still matches its
	// hash; an edited manifest.json would otherwise re-decide unchanged sealed
	// runs under new settings (audit 1-A-02).
	if err := utils.VerifyManifestHash(m); err != nil {
		verdict.Invalidate(an, "evidence: "+err.Error())
	}
	a.judgeCalibration(ctx, m, an, runs, policyOverride) // W8 judge calibration: judge_calibration instrument of judged experiments (judge.go)
	return an, nil
}

// releasePolicy evaluates the release policy over the analysis: the override
// file when one is given (a path from the command line), otherwise the policy
// the experiment froze (relative to the bench root), otherwise none. An
// override that cannot be loaded is an error. A frozen policy that cannot be
// loaded gives a blocking result instead, so a deleted or broken policy file
// can never let a release through, and the verdict page still renders.
// policyNone is the -policy value that turns release policy evaluation off.
const policyNone = "none"

// effectivePolicyFile is the release rule set that applies to m, the one
// choice analysis (releasePolicy) and the model switch report
// (migrationPolicy) both make: override "none" applies none; any other
// override applies that file; otherwise the rule set the experiment froze;
// otherwise the server's default policy when its owner set one; otherwise
// none. There is no implicit policies/default.yaml (N280). frozen says the
// file is the one the experiment froze, so its frozen hash applies.
func (a *app) effectivePolicyFile(m *schemas.Manifest, override string) (file string, frozen bool) {
	switch {
	case override == policyNone:
		return "", false
	case override != "":
		return override, m.Experiment.Policy != "" && filepath.Clean(override) == filepath.Clean(m.Experiment.Policy)
	case m.Experiment.Policy != "":
		return m.Experiment.Policy, true
	default:
		return a.defaultPolicy, false
	}
}

func (a *app) releasePolicy(m *schemas.Manifest, an *schemas.Analysis, runs []schemas.SealedRun, override string) (*schemas.PolicyResult, error) {
	file, _ := a.effectivePolicyFile(m, override)
	switch {
	case file == "":
		// No rule set chosen, or an explicit "none" that turns the frozen
		// one off for this check.
		return nil, nil
	case override != "":
		p, raw, err := policy.Load(override)
		if err != nil {
			return nil, err
		}
		// A deliberate override is a different file by design, so the frozen
		// hash (which guards the frozen file against drift) does not apply.
		// The result records the override's own hash in PolicyHash.
		mm := *m
		mm.PolicyHash = ""
		return withPolicyFile(policy.Evaluate(p, policy.Hash(raw), &mm, an, runs), file), nil
	case m.Experiment.Policy == "":
		p, raw, err := policy.Load(file)
		if err != nil {
			return nil, err
		}
		return withPolicyFile(policy.Evaluate(p, policy.Hash(raw), m, an, runs), file), nil
	}
	p, raw, err := policy.Load(a.cfg.Path(file))
	if err != nil {
		a.logger.Warn("release policy could not be loaded; the release is blocked", "experiment", m.ExpID, "policy", file, "error", err.Error())
		return policy.LoadFailure(file, m.PolicyHash, err), nil
	}
	return withPolicyFile(policy.Evaluate(p, policy.Hash(raw), m, an, runs), file), nil
}

// withPolicyFile sets the result's rule set file name from the path it was
// loaded from, the name every message prints.
func withPolicyFile(r *schemas.PolicyResult, file string) *schemas.PolicyResult {
	if r != nil {
		r.PolicyFile = policy.FileName(file)
	}
	return r
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

// sameAnalysis returns a saved analysis of an's experiment equal to an apart
// from its ID and creation time, with the same report, or nil.
func (a *app) sameAnalysis(an *schemas.Analysis, report string) *schemas.Analysis {
	want, err := analysisKey(an)
	if err != nil {
		return nil
	}
	base := filepath.Join(a.store.Root(), an.ExpID, "analysis")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "verdict.json"))
		if err != nil {
			continue
		}
		var old schemas.Analysis
		if json.Unmarshal(raw, &old) != nil || old.AnalysisID != e.Name() {
			continue
		}
		if k, err := analysisKey(&old); err != nil || k != want {
			continue
		}
		md, err := os.ReadFile(filepath.Join(dir, "report.md"))
		if err != nil || reportKey(string(md), &old) != reportKey(report, an) {
			continue
		}
		return &old
	}
	return nil
}

// analysisKey is an analysis as JSON without its ID and creation time.
func analysisKey(an *schemas.Analysis) (string, error) {
	c := *an
	c.AnalysisID, c.CreatedAt = "", time.Time{}
	if c.Policy != nil {
		pol := *c.Policy
		pol.EvaluatedAt = time.Time{}
		c.Policy = &pol
	}
	b, err := json.Marshal(&c)
	if err != nil {
		return "", err
	}
	// Sums over maps come out in any order, so a float can differ in its
	// last bits between two analyses of the same evidence: compare floats
	// at 12 significant digits.
	var tree any
	if err := json.Unmarshal(b, &tree); err != nil {
		return "", err
	}
	b, err = json.Marshal(roundFloats(tree))
	return string(b), err
}

// roundFloats rounds every number in a decoded JSON tree to 12 significant
// digits.
func roundFloats(v any) any {
	switch x := v.(type) {
	case float64:
		r, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'g', 12, 64), 64)
		return r
	case []any:
		for i := range x {
			x[i] = roundFloats(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = roundFloats(x[k])
		}
	}
	return v
}

// reportKey is a report without the analysis ID and the creation and
// policy evaluation times it names.
func reportKey(report string, an *schemas.Analysis) string {
	if an.AnalysisID != "" {
		report = strings.ReplaceAll(report, an.AnalysisID, "")
	}
	if !an.CreatedAt.IsZero() {
		report = strings.ReplaceAll(report, an.CreatedAt.UTC().Format(time.RFC3339), "")
	}
	if an.Policy != nil && !an.Policy.EvaluatedAt.IsZero() {
		report = strings.ReplaceAll(report, an.Policy.EvaluatedAt.UTC().Format(time.RFC3339), "")
	}
	return report
}
