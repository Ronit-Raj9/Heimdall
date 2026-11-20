package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/schemas"
	suitepkg "github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice" // X8 voice agents
)

// armOrderStream is the second PCG word. It is fixed so arm order depends on
// the experiment seed alone.
const armOrderStream = 0x62696672 // "bifr"

// AAArm is the arm id of the A/A arm (docs/noise-floor.md): a second copy of
// arm A, with A's config and config hash, that the planner adds on the clean
// condition when the experiment sets aa: true. The verdict reads it under the
// same id (verdict.ArmAA) to measure the noise floor.
const AAArm = "A2"

// aaSourceArm is the arm the A/A arm copies.
const aaSourceArm = "A"

// aaOrderStream is the PCG word of the A/A slot placement. It differs from
// armOrderStream so that adding aa: true never changes the A/B arm order.
const aaOrderStream = 0x61612d61 // "aa-a"

// Guard rules the planner accepts (DESIGN.md 9.2).
const (
	GuardNoRegression            = "no_regression"
	GuardNoNewIntegrityViolation = "no_new_integrity_violation"
	GuardTimeoutParity           = "timeout_parity"
)

// Default provider and scripted control names the planner reads from an
// agent config. They mirror gateways/bifrost (DefaultProvider) and
// adapters/scripted.go, which the engine does not import.
const (
	defaultGatewayProvider = "anthropic"
	adapterScripted        = "scripted"
	scriptedToy            = "toy"
)

// ErrUpstreamCheckUnsupported is what an UpstreamChecker returns, wrapped,
// when it cannot run the check at all (a fault proxy older than the check).
// The planner then warns and plans without it.
var ErrUpstreamCheckUnsupported = errors.New("upstream check is not supported")

// UpstreamChecker checks, from where a run's model calls leave the bench for
// the provider APIs, that each provider's real API can be reached.
// cmd/bifrost-bench wires the fault proxy's GET /upstreams.
type UpstreamChecker interface {
	CheckUpstreams(ctx context.Context) ([]UpstreamStatus, error)
}

// UpstreamStatus is one provider's check. A checker lists only providers
// whose calls go to the real API with a real key; a provider it does not
// list (scripted model, no key) has nothing to check.
type UpstreamStatus struct {
	Provider string
	Host     string // host:port dialled
	OK       bool
	Skipped  string // not checked, and why; never a failure
	Error    string
}

// PlannerOption configures a Planner.
type PlannerOption func(*Planner)

// WithUpstreamChecker makes Plan refuse a live experiment whose arms use a
// provider the checker reports unreachable. Without it no check runs.
func WithUpstreamChecker(c UpstreamChecker) PlannerOption {
	return func(p *Planner) { p.upstreams = c }
}

// Planner turns an experiment into a frozen, hashed manifest. Nothing runs.
type Planner struct {
	reg       *schemas.Registry
	benchRoot string
	logger    schemas.Logger
	upstreams UpstreamChecker // nil: no upstream check
	// agentImages builds per-task agent images (WithAgentImageBuilder); nil
	// picks one from the suite's environment.
	agentImages AgentImageBuilder
	prices      func(model string) bool // Forecast and matrix area: price snapshot lookup for matrix models (matrixplan.go)

	// Suite health area: explicit task lists may name quarantined tasks;
	// a worker's replan ignores quarantine (taskscreen.go).
	includeQuarantined bool
	ignoreQuarantine   bool

	guard        func(*schemas.Experiment) error // Tenancy area: hosted orgs confine experiment paths to the org's project (experimentguard.go)
	deferGateway bool                            // Tenancy area: a hosted server records the gateway preflight as the runners' (experimentguard.go)
	skipGPU      bool                            // X7: a remote worker's replan skips the GPU check (gpu.go)

	semanticCacheArm bool // B2: some arm of the experiment being planned sets gateway.bifrost.semantic_cache (bifrostfeatures.go)
}

// planMode is what differs between planning an experiment and planning an
// `agent check`: an experiment needs two arms, a locked suite (an experiment
// with smoke: true only warns) and, with a checker, reachable upstreams; a
// check runs one arm on the scripted model, so it needs none of that.
type planMode struct {
	minArms       int
	lockWarnOnly  bool
	skipUpstreams bool
}

// experimentMode is how Plan plans an experiment.
var experimentMode = planMode{minArms: 2}

// condPlan is one condition resolved against the suite.
type condPlan struct {
	id     string
	probe  *schemas.ProbeSpec // nil for clean
	tasks  []string
	trials int
}

// NewPlanner returns a planner. benchRoot anchors relative agent config and
// arm file paths (agents/claude-code.yaml, experiments/fixtures/...).
func NewPlanner(reg *schemas.Registry, benchRoot string, logger schemas.Logger, opts ...PlannerOption) *Planner {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	p := &Planner{reg: reg, benchRoot: benchRoot, logger: logger}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Plan validates the experiment against the suite and the registry, resolves
// every arm, lays out the slots and returns the hashed manifest.
//
// It refuses a gateway whose preflight found a hygiene violation (MCP tools
// auto-injected, semantic cache, multi-target routing), and records, with a
// warning, every budget that differs between arms without a `set:` override.
// With an UpstreamChecker it also refuses when a provider an arm calls is
// reported unreachable (checkUpstreams).
//
// It refuses a suite without a lock, or any selected task that the lock does
// not mark admitted, unless the experiment sets smoke: true (then it warns;
// the flag is part of the frozen experiment, so every later reader knows the
// runs are a synthetic smoke test). The experiment name relaxes nothing. It
// fails when two arms resolve to the same config hash, when an arm file and
// a workspace file (clean notes or a probe fixture) would be written to the
// same path, and when two overlays of one probe write the same workspace
// path. Arm order is drawn once per task from the seed and reused for every
// block of that task, so a task-level sign flip stays exact (DESIGN.md 9.3).
//
// With aa: true the plan also holds arm A2, a copy of arm A with the same
// config hash (the one same-hash pair allowed), on the clean condition only:
// one more counted slot per clean task and trial (resolveArms, buildSlots).
// The A/B arm order of every block is the one the same experiment without aa
// draws.
//
// Scenario tasks are checked against what the environment can do
// (checkScenario), their own images are built and recorded (taskImages), an
// agent with build.base_arg is built on each of them (taskAgentImages), and
// the suite-level scenario inputs are hashed (scenarioHashes); each is
// absent from a plan without scenario tasks.
func (p *Planner) Plan(ctx context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	return p.planMatrix(ctx, exp, experimentMode) // Forecast and matrix area: expands matrix sugar first, else exactly plan (matrixplan.go)
}

// plan is Plan under a plan mode. Each arm's image is resolved for the suite
// (ResolveAgentImage) before its config is hashed, and its local image id is
// recorded when the suite's environment can inspect images.
func (p *Planner) plan(ctx context.Context, exp *schemas.Experiment, mode planMode) (*schemas.Manifest, error) {
	if exp == nil {
		return nil, errors.New("plan: experiment is nil")
	}
	if p.reg == nil {
		return nil, errors.New("plan: registry is nil")
	}
	if p.guard != nil { // Tenancy area: refuse paths outside the org's project before anything is read (experimentguard.go)
		if err := p.guard(exp); err != nil {
			return nil, fmt.Errorf("plan %s: %w", exp.Name, err)
		}
	}
	if err := validateExperiment(exp, mode.minArms); err != nil {
		return nil, fmt.Errorf("plan %s: %w", exp.Name, err)
	}
	e := *exp
	if e.ID == "" {
		e.ID = newExpID(e.Name)
	}
	suite, err := LoadSuite(ctx, p.reg, e.Suite)
	if err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	notes, err := applySafetyTrifecta(suite, &e) // Safety probes area (S1): safety: trifecta (trifecta.go)
	if err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	for _, n := range notes { // Safety probes area (S1)
		p.logger.Info("plan safety", "experiment", e.ID, "note", n)
	}
	conds, err := resolveConditions(suite, e.Conditions)
	if err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	var tier *schemas.TierPlan
	if conds, tier, err = w7Conditions(suite, &e, conds); err != nil { // W7 suite tier and trials policy (adaptive.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	var compat []condPlan
	if e.Compatibility != nil {
		if compat, err = resolveConditions(suite, compatSpecs(e.Compatibility)); err != nil {
			return nil, fmt.Errorf("plan %s: compatibility: %w", e.ID, err)
		}
	}
	if conds, compat, err = p.screenAll(suite, &e, conds, compat, mode.lockWarnOnly); err != nil { // Suite health area: split and quarantine (taskscreen.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	all := append(append([]condPlan(nil), conds...), compat...)
	if err := p.checkLock(suite, e.Name, mode.lockWarnOnly || e.Smoke, all); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if err := checkFingerprints(ctx, p.reg, suite, p.logger, mode.lockWarnOnly || e.Smoke, "plan", e.ID); err != nil { // Lock fingerprints (fingerprints.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if e.Smoke {
		p.logger.Warn("smoke experiment: its runs are a synthetic smoke test, never findings", "experiment", e.ID)
	}
	if err := p.checkParts(&e, suite, all); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	m := &schemas.Manifest{
		ExpID:          e.ID,
		CreatedAt:      time.Now().UTC(),
		Experiment:     e,
		SuiteID:        suite.ID,
		SuiteVersion:   suite.Version,
		SuiteLockHash:  suite.LockHash,
		Parts:          PartVersions(p.reg),
		RandomizationU: RandomizationUnit,
		BenchVersion:   BenchVersion,
	}
	m.HeldOutTasks = heldOutTasks(suite, all) // Suite health area: held-out split (taskscreen.go)

	// Lock fingerprints (fingerprints.go): the exact evaluator settings.
	if m.FingerprintHash, err = lockFingerprintHash(suite); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if m.PolicyHash, err = p.policyHash(e.Policy); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if m.Arms, err = p.resolveArms(ctx, &e, suite, conds); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if err := checkFairness(&e, m.Arms, p.logger); err != nil { // Multi-agent area: equal-budget fairness (fairness.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if err := p.checkTaskTeams(m.Arms, suite, conds); err != nil { // X5b: a task's own team must fit the gateway's key slots (taskteam.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if m.BudgetParity = parityPtr(schemas.BudgetParityOf(&e, m.Arms)); m.BudgetParity.Status == schemas.BudgetParityUnexpectedDifference {
		return nil, fmt.Errorf("plan %s: unexpected budget difference: %s", e.ID, budgetParityText(*m.BudgetParity))
	}
	if gaps, err := checkCapabilities(&e.Requirements, m.Arms); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	} else {
		m.CapabilityGaps = gaps
		for armID, armGaps := range gaps {
			arm := m.Arms[armID]
			arm.CapabilityGaps = append([]string(nil), armGaps...)
			m.Arms[armID] = arm
		}
	}
	if m.TaskIdentities, err = p.taskIdentities(suite, all); err != nil {
		return nil, fmt.Errorf("plan %s: task identities: %w", e.ID, err)
	}
	m.BudgetDiffs = schemas.BudgetDiffsOf(&e, m.Arms)
	for _, d := range m.BudgetDiffs {
		p.logger.Warn("arm budgets differ without a set: override; the comparison includes the budget", "experiment", e.ID,
			"field", d.Field, "per_arm", budgetText(d))
	}
	if e.Compatibility != nil {
		c, err := p.resolveArm(ctx, CompatArm, e.Compatibility.Agent, nil, suite, compat)
		if err != nil {
			return nil, fmt.Errorf("plan %s: compatibility agent: %w", e.ID, err)
		}
		m.Compatibility = &c
		if gaps, err := checkCapabilities(&e.Requirements, map[string]schemas.ArmManifest{CompatArm: c}); err != nil {
			return nil, fmt.Errorf("plan %s: compatibility capabilities: %w", e.ID, err)
		} else if len(gaps[CompatArm]) > 0 {
			m.Compatibility.CapabilityGaps = append([]string(nil), gaps[CompatArm]...)
		}
	}
	if err := p.checkScenario(suite, all, m.Arms, m.Compatibility); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if m.Experiment.Environment, err = p.planDriver(&e, suite, all, m); err != nil { // X3: driver refusals, the frozen driver (driverplan.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	gpuArms := make([]schemas.ArmManifest, 0, len(m.Arms)+1)
	for _, id := range sortedKeys(m.Arms) {
		gpuArms = append(gpuArms, m.Arms[id])
	}
	if m.Compatibility != nil {
		gpuArms = append(gpuArms, *m.Compatibility)
	}
	if err := p.checkGPU(&e, suite, all, gpuArms...); err != nil { // X7: GPU tasks this machine cannot run are refused with the reason (gpu.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if err := p.checkExternal(&e, suite, all); err != nil { // External APIs area: modes and cassettes (externalplan.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if m.TaskImages, err = p.taskImages(ctx, suite, all); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	m.SuiteImages = p.suiteImages(ctx, suite) // Lock fingerprints: frozen suite image ids (suiteimages.go)
	if err := p.taskAgentImages(ctx, suite, conds, compat, m); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if m.Scenario, err = p.scenarioHashes(suite); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if err := planSimulator(&e, suite, all, m, p.logger); err != nil { // W3 customer support: user simulator checks and hash (simulatorplan.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	for _, c := range all { // X8 voice agents: voice tasks, voice probes and gateway.models are refused at plan time with the reason (bench/voice)
		if err := voice.CheckCondition(suite, c.probe, c.tasks); err != nil {
			return nil, fmt.Errorf("plan %s: condition %s: %w", e.ID, c.id, err)
		}
	}
	for _, arm := range sortedKeys(m.Arms) { // X8 voice agents
		if err := voice.CheckGatewayModels(m.Arms[arm].Config); err != nil {
			return nil, fmt.Errorf("plan %s: arm %s: %w", e.ID, arm, err)
		}
	}
	// B2: preflight accepts the semantic cache only for an experiment that tests it (bifrostfeatures.go).
	p.semanticCacheArm = declaresSemanticCache(m)
	if p.deferGateway { // Tenancy area: the runners preflight their own gateway before every slot (experimentguard.go)
		m.Gateway = deferredGateway()
	} else if m.Gateway, err = p.preflight(ctx, e.Name); err != nil {
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if m.Routing, err = checkRouting(m); err != nil { // W9 routing policy under test (routingplan.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if m.BifrostFeatures, err = p.checkBifrostFeatures(ctx, m, suite); err != nil { // B2 Bifrost features under test (bifrostfeatures.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if !mode.skipUpstreams {
		if err := p.checkUpstreams(ctx, m); err != nil {
			return nil, fmt.Errorf("plan %s: %w", e.ID, err)
		}
	}
	m.ArmOrder = drawArmOrder(e.Seed, suite.TaskIDs, conds, pairArms(m))
	m.Slots = buildSlots(m, suite, conds, compat)
	m.Tier = tier
	if m.Adaptive, err = planAdaptive(&e, m, conds, p.logger); err != nil { // W7 adaptive trials: optional slots of the maximum design (adaptive.go)
		return nil, fmt.Errorf("plan %s: %w", e.ID, err)
	}
	if m.RunInputs, err = p.runInputs(m, suite, all); err != nil {
		return nil, fmt.Errorf("plan %s: run inputs: %w", e.ID, err)
	}
	for i := range m.Slots {
		if input, ok := m.RunInputs[m.Slots[i].SlotID]; ok {
			m.Slots[i].RunInputHash = input.RunInputHash
		}
	}
	if m.Hash, err = ManifestHash(m); err != nil {
		return nil, err
	}
	return m, nil
}

// checkLock refuses an unlocked suite or a selected task that is not
// admitted. Only warnOnly relaxes it to warnings: an agent check, or an
// experiment that declares smoke: true, so the synthetic run works on a pack
// that is still being authored. The experiment name never relaxes it, since
// any experiment could be named "smoke-..."; a refused experiment whose name
// starts with "smoke" is told to set the field.
func (p *Planner) checkLock(suite *schemas.Suite, name string, warnOnly bool, conds []condPlan) error {
	var problems []string
	if suite.Lock == nil {
		problems = append(problems, fmt.Sprintf("suite %s@%s has no lock", suite.ID, suite.Version))
	} else {
		for _, id := range usedTasks(conds) {
			tl, ok := suite.Lock.Tasks[id]
			switch {
			case !ok:
				problems = append(problems, fmt.Sprintf("task %s is not in the suite lock", id))
			case tl.Status != schemas.TaskAdmitted:
				problems = append(problems, fmt.Sprintf("task %s is %s, not admitted", id, tl.Status))
			case !suitepkg.AdmissionCurrent(suite.Lock, id, tl.Admission):
				problems = append(problems, fmt.Sprintf("task %s admission was recorded for other lock fingerprints, so it is no longer admitted; run suite admit", id))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	if warnOnly {
		for _, pr := range problems {
			p.logger.Warn("smoke experiment or agent check ignores a suite lock problem", "experiment", name, "problem", pr)
		}
		return nil
	}
	if strings.HasPrefix(strings.ToLower(name), "smoke") {
		problems = append(problems, "the experiment name does not relax the lock; a synthetic smoke experiment sets smoke: true")
	}
	return fmt.Errorf("suite lock: %s", strings.Join(problems, "; "))
}

// checkParts fails early when a part the experiment needs is not registered.
func (p *Planner) checkParts(e *schemas.Experiment, suite *schemas.Suite, conds []condPlan) error {
	var errs []error
	if _, ok := findPart(p.reg.Environments, suite.Environment); !ok {
		errs = append(errs, fmt.Errorf("environment %q is not registered", suite.Environment))
	}
	if _, ok := findPart(p.reg.Graders, suite.Grader); !ok {
		errs = append(errs, fmt.Errorf("grader %q is not registered", suite.Grader))
	}
	for _, c := range conds {
		if c.probe == nil {
			continue
		}
		if _, ok := findPart(p.reg.ProbeLayers, string(c.probe.Layer)); !ok {
			errs = append(errs, fmt.Errorf("probe %s: layer %q is not registered", c.id, c.probe.Layer))
		}
		if c.probe.Layer == schemas.LayerAdapter {
			if p.reg.AdapterFaults == nil {
				errs = append(errs, fmt.Errorf("probe %s: adapter fault controller is not registered", c.id))
			}
			if c.probe.Apply.AdapterFault == nil {
				errs = append(errs, fmt.Errorf("probe %s: adapter_fault rule is required", c.id))
			} else if err := validateAdapterFaultSupport(p.reg, c.probe.Apply.AdapterFault, e); err != nil {
				errs = append(errs, fmt.Errorf("probe %s: %w", c.id, err))
			}
		}
	}
	for _, g := range e.ExtraGates {
		if _, ok := findPart(p.reg.Gates, g); !ok {
			errs = append(errs, fmt.Errorf("extra gate %q is not registered", g))
		}
	}
	errs = append(errs, codescan.CheckPolicy(e.CodeScan), codescan.CheckSuite(suite)) // X4: code scan veto level, ignored rules and external scanners
	known := map[string]bool{}
	for _, m := range selectMetrics(p.reg, e.Metrics) {
		known[m.GetName()], known[metricName(m)] = true, true
	}
	for _, n := range e.Metrics {
		if !known[n] {
			errs = append(errs, fmt.Errorf("metric %q is not registered", n))
		}
	}
	if !known[e.Primary.Metric] {
		errs = append(errs, fmt.Errorf("primary metric %q is not measured", e.Primary.Metric))
	}
	for _, g := range e.Guards {
		if g.Metric != "" && !known[g.Metric] {
			errs = append(errs, fmt.Errorf("guard metric %q is not measured", g.Metric))
		}
	}
	return errors.Join(errs...)
}

func validateAdapterFaultSupport(reg *schemas.Registry, rule *schemas.AdapterFaultRule, e *schemas.Experiment) error {
	if err := validateAdapterFaultRule(*rule); err != nil {
		return err
	}
	_ = reg
	_ = e
	return nil
}

func validateAdapterFaultRule(r schemas.AdapterFaultRule) error {
	if r.Kind != "tool" && r.Kind != "memory" && r.Kind != "browser" && r.Kind != "retry" {
		return fmt.Errorf("unsupported adapter fault kind %q", r.Kind)
	}
	if r.Name == "" {
		return fmt.Errorf("adapter fault operation name is required")
	}
	if r.Mode != "error" && r.Mode != "timeout" && r.Mode != "replace" {
		return fmt.Errorf("unsupported adapter fault mode %q", r.Mode)
	}
	if r.FromCall < 0 || r.Calls < 0 {
		return fmt.Errorf("adapter fault call bounds must be non-negative")
	}
	if r.Mode == "timeout" && (r.TimeoutMS < 1 || r.TimeoutMS > 3600000) {
		return fmt.Errorf("adapter fault timeout_ms must be 1..3600000")
	}
	if b, err := json.Marshal(r.Result); err != nil {
		return fmt.Errorf("invalid replacement: %w", err)
	} else if len(b) > 64<<10 {
		return fmt.Errorf("adapter fault replacement exceeds 64KiB")
	}
	return nil
}

// resolveArms resolves every arm and refuses arms that do not differ. With
// aa: true it then adds the A/A arm (aaArm), which is exempt from that
// refusal on purpose: two arms that do not differ are its measurement. The
// refusal still holds for every arm the experiment declares.
func (p *Planner) resolveArms(ctx context.Context, e *schemas.Experiment, suite *schemas.Suite, conds []condPlan) (map[string]schemas.ArmManifest, error) {
	out := map[string]schemas.ArmManifest{}
	byHash := map[string]string{}
	for _, id := range sortedKeys(e.Arms) {
		spec := e.Arms[id]
		cfgPath := spec.Agent
		if cfgPath == "" {
			cfgPath = e.Base
		}
		am, err := p.resolveArm(ctx, spec.Label, cfgPath, spec.Set, suite, conds)
		if err != nil {
			return nil, fmt.Errorf("arm %s: %w", id, err)
		}
		if prev, ok := byHash[am.ConfigHash]; ok {
			return nil, fmt.Errorf("arms %s and %s have the same config hash %s: an experiment whose arms do not differ cannot start", prev, id, am.ConfigHash)
		}
		byHash[am.ConfigHash] = id
		out[id] = am
	}
	if e.AA {
		a, ok := out[aaSourceArm]
		if !ok {
			return nil, fmt.Errorf("aa: no arm %s to copy", aaSourceArm)
		}
		out[AAArm] = aaArm(a)
	}
	return out, nil
}

// aaArm returns the A/A arm: resolved arm A with the same config, config
// hash, identity, budgets and image, labelled as the copy. It lays down only
// A's clean files, since it runs only on clean. Pointers and maps are copied
// so that no later step that edits one arm edits the other.
func aaArm(a schemas.ArmManifest) schemas.ArmManifest {
	c := a
	c.Label = "A/A copy of arm " + aaSourceArm
	if a.Label != "" {
		c.Label += " (" + a.Label + ")"
	}
	if a.BudgetSnapshot != nil {
		b := *a.BudgetSnapshot
		c.BudgetSnapshot = &b
	}
	if a.Identity != nil {
		id := *a.Identity
		if a.Identity.BudgetSnapshot != nil {
			id.BudgetSnapshot = c.BudgetSnapshot
		}
		c.Identity = &id
	}
	c.CapabilityGaps = slices.Clone(a.CapabilityGaps)
	c.TaskImages, c.TaskImageIDs = maps.Clone(a.TaskImages), maps.Clone(a.TaskImageIDs)
	c.Files = map[string]map[string]string{}
	if f, ok := a.Files[schemas.ConditionClean]; ok {
		c.Files[schemas.ConditionClean] = maps.Clone(f)
	}
	return c
}

// pairArms returns, sorted, the arms that share each block's pair: every
// resolved arm but the A/A arm, which sits in a slot of its own beside the
// pair. Without aa it is every arm, so the arm order draws are unchanged.
func pairArms(m *schemas.Manifest) []string {
	out := make([]string, 0, len(m.Arms))
	for _, id := range sortedKeys(m.Arms) {
		if m.Experiment.AA && id == AAArm {
			continue
		}
		out = append(out, id)
	}
	return out
}

// resolveArm reads one agent config, applies the overrides, resolves its
// image for the suite, hashes it and hashes every file laid down for it per
// condition. The image is resolved before hashing, so an agent with
// build.base_arg hashes its per-suite tag and an agent without it hashes
// exactly what it did before.
func (p *Planner) resolveArm(ctx context.Context, label, cfgPath string, set map[string]any, suite *schemas.Suite, conds []condPlan) (schemas.ArmManifest, error) {
	var am schemas.ArmManifest
	raw, err := os.ReadFile(p.abs(cfgPath))
	if err != nil {
		return am, fmt.Errorf("read agent config: %w", err)
	}
	cfg, err := ResolveAgentConfig(raw, set)
	if err != nil {
		return am, fmt.Errorf("agent config %s: %w", cfgPath, err)
	}
	if err := p.checkAgent(&cfg); err != nil {
		return am, fmt.Errorf("agent config %s: %w", cfgPath, err)
	}
	if err := p.resolveTeam(ctx, &cfg, suite); err != nil { // Multi-agent area: member configs frozen with the arm (team.go)
		return am, fmt.Errorf("agent config %s: %w", cfgPath, err)
	}
	if err := checkA2AArm(&cfg, conds); err != nil { // B1: a2a agents and the a2a probe layer (a2a.go)
		return am, fmt.Errorf("agent config %s: %w", cfgPath, err)
	}
	for _, c := range conds {
		if c.probe == nil || c.probe.Layer != schemas.LayerAdapter || c.probe.Apply.AdapterFault == nil {
			continue
		}
		wall := cfg.Budgets.WallS * 1000
		if err := utils.ValidateAdapterFault(*c.probe.Apply.AdapterFault, wall); err != nil {
			return am, fmt.Errorf("adapter fault rule: %w", err)
		}
		if cfg.Adapter == "remote_http" {
			return am, fmt.Errorf("agent config %s: remote_http agents are not supported by adapter fault hooks", cfgPath)
		}
		injector, ok := findPart(p.reg.Adapters, cfg.Adapter)
		if !ok {
			return am, fmt.Errorf("agent adapter %q is not registered", cfg.Adapter)
		}
		if fi, ok := injector.(schemas.AdapterFaultInjector); ok {
			if err := fi.ValidateFault(&cfg, *c.probe.Apply.AdapterFault); err != nil {
				return am, fmt.Errorf("adapter fault capability: %w", err)
			}
		} else {
			return am, fmt.Errorf("adapter %s does not support adapter fault hooks", cfg.Adapter)
		}
	}
	img, err := ResolveAgentImage(&cfg, suite, p.benchRoot)
	if err != nil {
		return am, fmt.Errorf("agent config %s: %w", cfgPath, err)
	}
	cfg.Image = img.Tag
	hash, err := utils.HashJSON(&cfg)
	if err != nil {
		return am, fmt.Errorf("hash agent config %s: %w", cfgPath, err)
	}
	files, err := p.layFiles(&cfg, suite, conds)
	if err != nil {
		return am, err
	}
	members, err := p.memberManifests(ctx, &cfg, suite, conds)
	if err != nil {
		return am, err
	}
	snapshot := effectiveBudgetSnapshot(&cfg, suite, conds)
	identity := p.armIdentity(&cfg, hash)
	identity.BudgetSnapshot = &snapshot
	return schemas.ArmManifest{Label: label, Config: cfg, ConfigHash: hash, Identity: &identity, BudgetSnapshot: &snapshot, Files: files, ImageID: p.imageID(ctx, suite, cfg.Image), Members: members}, nil
}

// memberManifests freezes the image id and laid files of every team member
// that runs a config of its own, so a rebuilt member tag or a changed member
// notes or skill file is bound to the manifest like the arm's own. A member
// without a config of its own runs the arm's and is covered by the arm.
func (p *Planner) memberManifests(ctx context.Context, cfg *schemas.AgentConfig, suite *schemas.Suite, conds []condPlan) (map[string]schemas.MemberManifest, error) {
	var out map[string]schemas.MemberManifest
	for _, m := range cfg.Team {
		if m.Config == nil {
			continue
		}
		files, err := p.layFiles(m.Config, suite, conds)
		if err != nil {
			return nil, fmt.Errorf("team member %s: %w", m.ID, err)
		}
		if out == nil {
			out = map[string]schemas.MemberManifest{}
		}
		out[m.ID] = schemas.MemberManifest{ImageID: p.imageID(ctx, suite, m.Config.Image), Files: files}
	}
	return out, nil
}

// imageID returns the local image id of ref when the suite's environment can
// inspect images, or "" with a warning when it cannot be read. Planning never
// needs the image to exist: a missing image fails the run, not the plan.
func (p *Planner) imageID(ctx context.Context, suite *schemas.Suite, ref string) string {
	if ref == "" {
		return ""
	}
	env, ok := findPart(p.reg.Environments, suite.Environment)
	if !ok {
		return ""
	}
	ins, ok := any(env).(ImageInspector)
	if !ok {
		return ""
	}
	id, err := ins.ImageID(ctx, ref)
	if err != nil {
		p.logger.Warn("agent image not inspected; the manifest records no image id", "image", ref, "error", err.Error())
		return ""
	}
	return id
}

func (p *Planner) armIdentity(cfg *schemas.AgentConfig, hash string) schemas.CanonicalArmIdentity {
	identity := schemas.CanonicalArmIdentity{
		AgentConfigID: cfg.ID, AgentConfigHash: hash, AgentVersion: cfg.AgentVersion,
		ImageDigest: cfg.ImageDigest, ModelRequested: cfg.Model, GatewayProvider: cfg.Gateway.Provider,
		GatewayPolicy: cfg.Gateway, Budgets: cfg.Budgets, Capabilities: cfg.Capabilities,
	}
	if adapter, ok := findPart(p.reg.Adapters, cfg.Adapter); ok {
		identity.AdapterVersion = adapter.GetVersion()
	}
	if cfg.Parser != "" {
		if parser, ok := findPart(p.reg.Parsers, cfg.Parser); ok {
			identity.ParserVersion = parser.GetVersion()
		}
	}
	return identity
}

func parityPtr(v schemas.BudgetParityResult) *schemas.BudgetParityResult { return &v }

func budgetParityText(p schemas.BudgetParityResult) string {
	parts := make([]string, 0, len(p.ConfiguredDiffs)+len(p.EffectiveDiffs))
	for _, diff := range append(append([]schemas.BudgetDiff(nil), p.ConfiguredDiffs...), p.EffectiveDiffs...) {
		parts = append(parts, budgetText(diff))
	}
	return strings.Join(parts, "; ")
}

func effectiveBudgetSnapshot(cfg *schemas.AgentConfig, suite *schemas.Suite, conds []condPlan) schemas.BudgetSnapshot {
	effective := cfg.Budgets
	sources := map[string]string{
		"wall_s": "agent_config", "turns": "agent_config", "cpus": "agent_config", "mem_gb": "agent_config",
		"pids_limit": "agent_config", "usd": "agent_config", "max_retries": "agent_config", "max_tool_calls": "agent_config",
	}
	if effective.MaxRetries == 0 && cfg.Gateway.MaxRetries > 0 {
		effective.MaxRetries = cfg.Gateway.MaxRetries
		sources["max_retries"] = "gateway_policy"
	}
	minTaskWall := 0
	if suite != nil {
		for _, cond := range conds {
			for _, id := range cond.tasks {
				task := suite.TaskByID(id)
				if task == nil || task.Timeouts.AgentS <= 0 {
					continue
				}
				if minTaskWall == 0 || task.Timeouts.AgentS < minTaskWall {
					minTaskWall = task.Timeouts.AgentS
				}
			}
		}
	}
	if minTaskWall > 0 && (effective.WallS <= 0 || minTaskWall < effective.WallS) {
		effective.WallS = minTaskWall
		sources["wall_s"] = "task_limit"
	}
	return schemas.BudgetSnapshot{Configured: cfg.Budgets, Effective: effective, Source: sources}
}

func checkCapabilities(req *schemas.CapabilityRequirements, arms map[string]schemas.ArmManifest) (map[string][]string, error) {
	gaps := map[string][]string{}
	if req == nil {
		return gaps, nil
	}
	for armID, arm := range arms {
		for _, name := range req.Required {
			if !capabilitySupported(arm.Config.Capabilities, name) {
				return nil, fmt.Errorf("arm %s lacks required capability %q", armID, name)
			}
		}
		for _, name := range req.Optional {
			if !capabilitySupported(arm.Config.Capabilities, name) {
				gaps[armID] = append(gaps[armID], name)
			}
		}
	}
	return gaps, nil
}

func capabilitySupported(c *schemas.AdapterCapabilities, name string) bool {
	if c == nil {
		return false
	}
	switch name {
	case "streaming":
		return c.SupportsStreaming
	case "structured_events":
		return c.SupportsStructuredEvents
	case "tool_event_ids":
		return c.SupportsToolEvents
	case "notes_file":
		return c.SupportsNotesFile
	case "model_override":
		return c.SupportsModelOverride
	case "cancellation":
		return c.SupportsCancellation
	case "resume":
		return c.SupportsResume
	case "gateway_headers":
		return c.SupportsGatewayHeaders
	case "external_tool_runner":
		return c.SupportsToolEvents
	default:
		return false
	}
}

type taskHashInput struct {
	SuiteID          string                `json:"suite_id"`
	SuiteVersion     string                `json:"suite_version"`
	Task             schemas.Task          `json:"task"`
	TaskPackageHash  string                `json:"task_package_hash"`
	TaskLock         taskContentHashes     `json:"task_lock"`
	SuiteEnvironment string                `json:"suite_environment"`
	SuiteWorkdir     string                `json:"suite_workdir"`
	SuiteBaseImage   schemas.ImageSpec     `json:"suite_base_image"`
	SuiteVerifier    schemas.SuiteVerifier `json:"suite_verifier"`
	GraderVersion    string                `json:"grader_version"`
	RequiredEnv      []string              `json:"required_environment,omitempty"`
}

type taskContentHashes struct {
	Instruction string `json:"instruction"`
	Repo        string `json:"repo"`
	Verifier    string `json:"verifier"`
	Controls    string `json:"controls"`
	Fixtures    string `json:"fixtures"`
}

// taskHash returns the identity of the task package. It excludes conditions,
// probes, arms, models and run-specific values; those belong to run_input_hash.
func (p *Planner) taskHash(suite *schemas.Suite, task *schemas.Task) (string, error) {
	if suite == nil || task == nil {
		return "", errors.New("task hash: suite and task are required")
	}
	packageHash, err := utils.CanonicalTreeHash(task.Dir)
	if err != nil {
		return "", fmt.Errorf("hash task package %s: %w", task.ID, err)
	}
	var hashes taskContentHashes
	if suite.Lock != nil {
		lock := suite.Lock.Tasks[task.ID]
		hashes = taskContentHashes{Instruction: lock.InstructionSHA256, Repo: lock.RepoTreeSHA256,
			Verifier: lock.VerifierSHA256, Controls: lock.ControlsSHA256, Fixtures: lock.FixturesSHA256}
	}
	graderVersion := ""
	if grader, ok := findPart(p.reg.Graders, suite.Grader); ok {
		graderVersion = grader.GetVersion()
	}
	required := append([]string(nil), task.Verifier.Required...)
	sort.Strings(required)
	input := taskHashInput{SuiteID: suite.ID, SuiteVersion: suite.Version, Task: *task, TaskPackageHash: packageHash, TaskLock: hashes,
		SuiteEnvironment: suite.Environment, SuiteWorkdir: suite.Workdir, SuiteBaseImage: suite.BaseImage,
		SuiteVerifier: suite.Verifier, GraderVersion: graderVersion, RequiredEnv: required}
	hash, err := utils.HashJSON(input)
	if err != nil {
		return "", fmt.Errorf("hash task %s: %w", task.ID, err)
	}
	return hash, nil
}

func (p *Planner) taskIdentities(suite *schemas.Suite, conds []condPlan) ([]schemas.CanonicalTaskIdentity, error) {
	var out []schemas.CanonicalTaskIdentity
	for _, c := range conds {
		for trial := 1; trial <= c.trials; trial++ {
			for _, id := range c.tasks {
				task := suite.TaskByID(id)
				hash, err := p.taskHash(suite, task)
				if err != nil {
					return nil, err
				}
				out = append(out, schemas.CanonicalTaskIdentity{SuiteID: suite.ID, SuiteVersion: suite.Version,
					TaskID: task.ID, TaskVersion: task.Version, TaskHash: hash, Condition: c.id, Trial: trial})
			}
		}
	}
	return out, nil
}

func taskHashFor(m *schemas.Manifest, task, condition string) string {
	for _, identity := range m.TaskIdentities {
		if identity.TaskID == task && identity.Condition == condition {
			return identity.TaskHash
		}
	}
	return ""
}

func (p *Planner) runInputs(m *schemas.Manifest, suite *schemas.Suite, conds []condPlan) (map[string]schemas.RunInputIdentity, error) {
	inputs := make(map[string]schemas.RunInputIdentity, len(m.Slots))
	for i := range m.Slots {
		slot := &m.Slots[i]
		arm, err := armConfig(m, slot)
		if err != nil {
			return nil, err
		}
		probe := suite.ProbeByID(slot.Condition)
		probeHash := ""
		if probe != nil {
			probeHash, err = utils.HashJSON(probe)
			if err != nil {
				return nil, fmt.Errorf("hash probe %s: %w", probe.ID, err)
			}
		}
		policyHash, err := utils.HashJSON(arm.Config.Gateway)
		if err != nil {
			return nil, fmt.Errorf("hash gateway policy for arm %s: %w", slot.Arm, err)
		}
		envHash, err := environmentPolicyHash(arm.Config.Env)
		if err != nil {
			return nil, fmt.Errorf("hash environment policy for arm %s: %w", slot.Arm, err)
		}
		identity := schemas.RunInputIdentity{TaskHash: taskHashFor(m, slot.Task, slot.Condition), Condition: slot.Condition,
			ProbeHash: probeHash, ArmConfigHash: arm.ConfigHash, ModelRequested: arm.Config.Model,
			GatewayProvider: arm.Config.Gateway.Provider, GatewayPolicyHash: policyHash, EnvironmentPolicyHash: envHash}
		hash, err := utils.HashJSON(struct {
			Identity schemas.RunInputIdentity `json:"identity"`
			Budgets  schemas.Budgets          `json:"budgets"`
			Suite    string                   `json:"suite"`
			Workdir  string                   `json:"workdir"`
		}{Identity: identity, Budgets: arm.Config.Budgets, Suite: suite.Environment, Workdir: suite.Workdir})
		if err != nil {
			return nil, fmt.Errorf("hash run input for slot %s: %w", slot.SlotID, err)
		}
		identity.RunInputHash = hash
		inputs[slot.SlotID] = identity
	}
	return inputs, nil
}

func environmentPolicyHash(env map[string]string) (string, error) {
	policy := make(map[string]string, len(env))
	for name, value := range env {
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "AUTH") {
			policy[name] = "[redacted]"
			continue
		}
		policy[name] = value
	}
	return utils.HashJSON(policy)
}

func (p *Planner) checkAgent(cfg *schemas.AgentConfig) error {
	if cfg.ID == "" {
		return errors.New("id is empty")
	}
	if _, ok := findPart(p.reg.Adapters, cfg.Adapter); !ok {
		return fmt.Errorf("adapter %q is not registered", cfg.Adapter)
	}
	if cfg.Parser != "" {
		if _, ok := findPart(p.reg.Parsers, cfg.Parser); !ok {
			return fmt.Errorf("parser %q is not registered", cfg.Parser)
		}
	}
	if cfg.Notes.UserFile != "" && !path.IsAbs(cfg.Notes.UserPath) {
		return fmt.Errorf("notes.user_file is set but notes.user_path %q is not an absolute container path", cfg.Notes.UserPath)
	}
	return nil
}

// layFiles returns condition -> path -> sha256 for every file laid down for
// an arm. Arm files (notes.user_file at notes.user_path, files:) are keyed
// by container path; workspace files (clean notes, probe fixtures) differ per
// task and are keyed "<task>:<container path>", and a probe fixture appended
// to the user-level notes "<task>:append:<n>:<notes.user_path>" (appendKey).
// Laying down follows DESIGN.md 8.6: arm files first, then the clean notes,
// then probe fixtures overwrite.
func (p *Planner) layFiles(cfg *schemas.AgentConfig, suite *schemas.Suite, conds []condPlan) (map[string]map[string]string, error) {
	armFiles := map[string]string{}
	if cfg.Notes.UserFile != "" {
		sum, err := utils.SHA256File(p.abs(cfg.Notes.UserFile))
		if err != nil {
			return nil, fmt.Errorf("notes.user_file: %w", err)
		}
		armFiles[path.Clean(cfg.Notes.UserPath)] = sum
	}
	for dst, tmpl := range cfg.Files {
		armFiles[path.Clean(dst)] = utils.SHA256Bytes([]byte(tmpl))
	}
	out := map[string]map[string]string{}
	for _, c := range conds {
		files := map[string]string{}
		for k, v := range armFiles {
			files[k] = v
		}
		for _, id := range c.tasks {
			ws, err := workspaceFiles(suite, suite.TaskByID(id), c.probe, cfg)
			if err != nil {
				return nil, fmt.Errorf("condition %s, task %s: %w", c.id, id, err)
			}
			for dst, sum := range ws {
				if _, clash := armFiles[dst]; clash {
					return nil, fmt.Errorf("condition %s, task %s: an arm file and a workspace file (notes or probe fixture) both write %s", c.id, id, dst)
				}
				files[id+":"+dst] = sum
			}
			if err := adapters.AddSkillHashes(files, p.benchRoot, cfg, suite.TaskByID(id)); err != nil { // agent wiring: skill content in the manifest
				return nil, fmt.Errorf("condition %s, task %s: %w", c.id, id, err)
			}
		}
		out[c.id] = files
	}
	return out, nil
}

// preflight checks the gateway. Problems are warnings, but a hygiene
// violation refuses the plan: a gateway feature that rewrites, caches or
// reroutes a run's model requests would make the arms measure the gateway
// instead of the agent, and no analysis can undo that after the runs.
func (p *Planner) preflight(ctx context.Context, name string) (*schemas.GatewayManifest, error) {
	if p.reg.Gateway == nil {
		return nil, errors.New("no gateway registered")
	}
	gm, err := p.reg.Gateway.Preflight(ctx)
	if err != nil {
		return nil, fmt.Errorf("gateway preflight: %w", err)
	}
	for _, pr := range gm.Problems {
		p.logger.Warn("gateway preflight problem", "experiment", name, "problem", pr)
	}
	if p.semanticCacheArm { // B2 Bifrost features: an arm opts in to the semantic cache with its own key header (bifrostfeatures.go)
		acceptSemanticCache(gm.Hygiene)
	}
	if h := gm.Hygiene; h != nil && len(h.Violations) > 0 {
		return nil, fmt.Errorf("gateway hygiene check failed, fix the gateway config before planning: %s", strings.Join(h.Violations, "; "))
	}
	return gm, nil
}

// checkUpstreams refuses to freeze a live experiment whose model calls
// would fail before any agent sees them: a provider an arm calls is answered
// by its real API with a real key (the checker lists it) and the check
// cannot reach it. Live is decided per provider and per arm. A provider the
// checker does not list, or lists as skipped, is answered by the scripted
// model or not checkable, so it is not live. An arm that cannot make a model
// call (a scripted control other than toy, which runs with no network) uses
// no provider. With no checker, or no arm that calls a provider, nothing is
// checked; a checker that cannot run the check (an older fault proxy) only
// warns. Any other checker error refuses the plan: every run binds a plan
// through the same control API, so it would fail too.
func (p *Planner) checkUpstreams(ctx context.Context, m *schemas.Manifest) error {
	if p.upstreams == nil {
		return nil
	}
	used := armProviders(m)
	if len(used) == 0 {
		return nil
	}
	statuses, err := p.upstreams.CheckUpstreams(ctx)
	if errors.Is(err, ErrUpstreamCheckUnsupported) {
		p.logger.Warn("upstream check not supported; planning without it", "experiment", m.ExpID, "error", err.Error())
		return nil
	}
	if err != nil {
		return fmt.Errorf("upstream check: %w", err)
	}
	var problems []string
	for _, st := range statuses {
		if st.OK || st.Skipped != "" {
			continue
		}
		arms, ok := used[st.Provider]
		if !ok {
			p.logger.Warn("upstream unreachable for a provider no arm uses", "experiment", m.ExpID,
				"provider", st.Provider, "host", st.Host, "error", st.Error)
			continue
		}
		problems = append(problems, fmt.Sprintf("provider %s (%s, arms %s): %s", st.Provider, st.Host, strings.Join(arms, ", "), st.Error))
	}
	if len(problems) > 0 {
		return fmt.Errorf("upstream check failed, every model call would fail; fix the network or the provider key before planning: %s",
			strings.Join(problems, "; "))
	}
	return nil
}

// armProviders maps each provider a run of m can call to the arms that call
// it, the compatibility agent included. An arm calls its gateway provider
// (anthropic when the config names none, as the gateway defaults) unless it
// is a scripted control other than toy.
func armProviders(m *schemas.Manifest) map[string][]string {
	out := map[string][]string{}
	add := func(arm string, cfg *schemas.AgentConfig) {
		if cfg.Adapter == adapterScripted && (cfg.Scripted == nil || (cfg.Scripted.Kind != scriptedToy && cfg.Scripted.Kind != adapters.ScriptedMCPLoop)) { // B2: the MCP loop agent calls the gateway too
			return
		}
		prov := cfg.Gateway.Provider
		if prov == "" {
			prov = defaultGatewayProvider
		}
		out[prov] = append(out[prov], arm)
	}
	for _, id := range sortedKeys(m.Arms) {
		am := m.Arms[id]
		add(id, &am.Config)
	}
	if m.Compatibility != nil {
		add(CompatArm, &m.Compatibility.Config)
	}
	return out
}

// budgetText is "A=2, B=1" for a log line.
func budgetText(d schemas.BudgetDiff) string {
	parts := make([]string, 0, len(d.PerArm))
	for _, arm := range sortedKeys(d.PerArm) {
		parts = append(parts, arm+"="+d.PerArm[arm])
	}
	return strings.Join(parts, ", ")
}

// policyHash freezes the release policy the experiment names: the sha256 of
// the file's raw bytes, so a policy edited after planning is detected at
// analysis. No policy gives no hash; a named policy that cannot be read fails
// the plan, because the experiment would otherwise run without the rules it
// declared.
func (p *Planner) policyHash(rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	sum, err := utils.SHA256File(p.abs(rel))
	if err != nil {
		return "", fmt.Errorf("release policy %s: %w", rel, err)
	}
	return sum, nil
}

func (p *Planner) abs(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(p.benchRoot, rel)
}

// userNotesTarget is the one overlay target that is not a workspace path:
// the agent's user-level notes file (probes/fixture.go appends there).
var userNotesTarget = regexp.MustCompile(`^\{\{\s*agent\.notes\.user_path\s*\}\}$`)

// workspaceFiles returns what is laid down for one task under one condition,
// for one agent config, as key -> sha256 of the source file: the clean notes
// and every workspace overlay by container path, and every fixture appended
// to the agent's user-level notes by appendKey. A fixture probe's overlays
// are all hashed, the single Overlay first and then Overlays in order, as the
// fixture layer applies them; two overlays writing one workspace path are an
// error, since only the last would reach the agent. A probe with only the
// single Overlay hashes exactly what it always did.
func workspaceFiles(suite *schemas.Suite, task *schemas.Task, probe *schemas.ProbeSpec, cfg *schemas.AgentConfig) (map[string]string, error) {
	workdir := suite.Workdir
	if workdir == "" {
		workdir = "/workspace"
	}
	out := map[string]string{}
	if task.Notes.Clean != "" && cfg.Notes.ProjectFile != "" {
		dst, err := containerPath(workdir, cfg.Notes.ProjectFile)
		if err != nil {
			return nil, fmt.Errorf("notes.project_file: %w", err)
		}
		src, err := hostPath(task.Dir, task.Notes.Clean)
		if err != nil {
			return nil, fmt.Errorf("clean notes: %w", err)
		}
		if out[dst], err = utils.SHA256File(src); err != nil {
			return nil, fmt.Errorf("clean notes: %w", err)
		}
	}
	if probe == nil || probe.Layer != schemas.LayerFixture {
		return out, nil
	}
	var specs []schemas.OverlaySpec
	if probe.Apply.Overlay != nil {
		specs = append(specs, *probe.Apply.Overlay)
	}
	specs = append(specs, probe.Apply.Overlays...)
	written := map[string]int{} // workspace path -> overlay number
	appends := 0
	for i, spec := range specs {
		n := i + 1
		key, src, err := overlayFile(suite, task, cfg, workdir, spec)
		if err != nil {
			return nil, fmt.Errorf("probe %s overlay %d: %w", probe.ID, n, err)
		}
		if key == "" && i == 0 && probe.Apply.Overlay != nil {
			return nil, fmt.Errorf("probe %s overlay 1: the user-level notes target is allowed in overlays only", probe.ID)
		}
		if key == "" {
			appends++
			key = appendKey(cfg.Notes.UserPath, appends)
		} else if prev, dup := written[key]; dup {
			return nil, fmt.Errorf("probe %s: overlays %d and %d both write %s; only the last would reach the agent", probe.ID, prev, n, key)
		} else {
			written[key] = n
		}
		if out[key], err = utils.SHA256File(src); err != nil {
			return nil, fmt.Errorf("probe %s fixture: %w", probe.ID, err)
		}
	}
	return out, nil
}

// overlayFile resolves one overlay the way probes/fixture.go does and returns
// its workspace container path ("" for an append to the user-level notes)
// and the host path of its fixture. Path variables are {{task.id}},
// {{task.project}}, {{task.notes.clean}}, {{task.notes.stale}},
// {{agent.notes.project_file}} and {{suite.root}}; a from that renders
// absolute must lie inside the suite folder, any other is relative to the
// task folder.
func overlayFile(suite *schemas.Suite, task *schemas.Task, cfg *schemas.AgentConfig, workdir string, spec schemas.OverlaySpec) (string, string, error) {
	vars := map[string]string{
		"task.id":                  task.ID,
		"task.project":             task.Project,
		"task.notes.clean":         task.Notes.Clean,
		"task.notes.stale":         task.Notes.Stale,
		"agent.notes.project_file": cfg.Notes.ProjectFile,
	}
	if suite.Root != "" {
		vars["suite.root"] = filepath.ToSlash(filepath.Clean(suite.Root))
	}
	from, err := utils.Render(spec.From, vars)
	if err != nil {
		return "", "", fmt.Errorf("from: %w", err)
	}
	from = strings.TrimSpace(from)
	if from == "" {
		return "", "", fmt.Errorf("from %q resolves to an empty path", spec.From)
	}
	var src string
	if abs := filepath.FromSlash(from); filepath.IsAbs(abs) && suite.Root != "" {
		rel, err := filepath.Rel(filepath.Clean(suite.Root), filepath.Clean(abs))
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			return "", "", fmt.Errorf("from %q must be inside the suite folder", from)
		}
		src = filepath.Join(suite.Root, rel)
	} else if src, err = hostPath(task.Dir, from); err != nil {
		return "", "", fmt.Errorf("from: %w", err)
	}
	if userNotesTarget.MatchString(strings.TrimSpace(spec.To)) {
		if !path.IsAbs(strings.TrimSpace(cfg.Notes.UserPath)) {
			return "", "", fmt.Errorf("to {{agent.notes.user_path}}: agent %s sets no absolute notes.user_path", cfg.ID)
		}
		return "", src, nil
	}
	to, err := utils.Render(spec.To, vars)
	if err != nil {
		return "", "", fmt.Errorf("to: %w", err)
	}
	if strings.TrimSpace(to) == "" {
		return "", "", fmt.Errorf("to %q resolves to an empty path", spec.To)
	}
	dst, err := containerPath(workdir, strings.TrimSpace(to))
	if err != nil {
		return "", "", fmt.Errorf("to: %w", err)
	}
	return dst, src, nil
}

// appendKey is the files-map key of the n-th fixture a probe appends to the
// agent's user-level notes, "append:<n>:<notes.user_path>". It never equals
// a container path, so it cannot clash with the arm's own notes.user_file,
// which the append extends rather than replaces.
func appendKey(userPath string, n int) string {
	return fmt.Sprintf("append:%d:%s", n, path.Clean(strings.TrimSpace(userPath)))
}

// containerPath joins a relative path under a container root.
func containerPath(root, rel string) (string, error) {
	c, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	return path.Join(root, c), nil
}

// hostPath joins a relative path under a host folder.
func hostPath(root, rel string) (string, error) {
	c, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(c)), nil
}

// cleanRel refuses absolute paths and paths that climb out of their root.
func cleanRel(rel string) (string, error) {
	if rel == "" || path.IsAbs(rel) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative", rel)
	}
	c := path.Clean(filepath.ToSlash(rel))
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path %q leaves its root", rel)
	}
	return c, nil
}

// validateExperiment checks the shape of an experiment before any I/O.
// minArms is 2 for an experiment and 1 for an agent check.
func validateExperiment(e *schemas.Experiment, minArms int) error {
	var errs []error
	if e.Name == "" {
		errs = append(errs, errors.New("name is empty"))
	}
	if e.Suite == "" {
		errs = append(errs, errors.New("suite is empty"))
	}
	if e.ID != "" && !validID(e.ID) {
		errs = append(errs, fmt.Errorf("experiment id %q must match ^[a-z0-9_]+$", e.ID))
	}
	if len(e.Arms) < minArms {
		errs = append(errs, fmt.Errorf("need at least %d arms, got %d", minArms, len(e.Arms)))
	}
	for id, a := range e.Arms {
		if id == CompatArm {
			errs = append(errs, fmt.Errorf("arm id %q is reserved for compatibility runs", CompatArm))
		}
		if a.Agent == "" && e.Base == "" {
			errs = append(errs, fmt.Errorf("arm %s has no agent config and the experiment has no base", id))
		}
	}
	if len(e.Conditions) == 0 {
		errs = append(errs, errors.New("no conditions"))
	}
	conds := map[string]bool{}
	for _, c := range e.Conditions {
		conds[c.ID] = true
	}
	if !conds[e.Primary.Condition] {
		errs = append(errs, fmt.Errorf("primary condition %q is not one of the conditions", e.Primary.Condition))
	}
	if e.Primary.Metric == "" {
		errs = append(errs, errors.New("primary metric is empty"))
	}
	for _, g := range e.Guards {
		switch g.Rule {
		case GuardNoRegression:
			if !conds[g.Condition] {
				errs = append(errs, fmt.Errorf("guard %s: condition %q is not one of the conditions", g.Rule, g.Condition))
			}
		case GuardNoNewIntegrityViolation:
		case GuardTimeoutParity:
			if g.Condition != "" && !conds[g.Condition] {
				errs = append(errs, fmt.Errorf("guard %s: condition %q is not one of the conditions", g.Rule, g.Condition))
			}
		default:
			errs = append(errs, fmt.Errorf("unknown guard rule %q", g.Rule))
		}
	}
	if e.Concurrency < 0 {
		errs = append(errs, fmt.Errorf("concurrency %d is negative", e.Concurrency))
	}
	if e.Budget.TotalUSD < 0 {
		errs = append(errs, fmt.Errorf("budget total_usd %v is negative", e.Budget.TotalUSD))
	}
	if e.Runner != nil && e.Runner.Pool != "" && !schemas.ValidPoolName(e.Runner.Pool) { // Runners area: runner.pool (docs/runners.md)
		errs = append(errs, fmt.Errorf("runner pool %q must be 1 to 64 letters, digits, '.', '_' or '-'", e.Runner.Pool))
	}
	if c := e.Compatibility; c != nil && (c.Agent == "" || c.Trials < 1 || len(c.Conditions) == 0) {
		errs = append(errs, errors.New("compatibility needs an agent, conditions and trials >= 1"))
	}
	errs = append(errs, aaErrors(e, conds)...)
	if r := e.Realism; r != "" && r != schemas.RealismStandard && r != schemas.RealismNeutral { // Eval integrity area (S2): W-A realism check
		errs = append(errs, fmt.Errorf("realism %q must be standard or neutral", r)) // Eval integrity area (S2)
	} // Eval integrity area (S2)
	return errors.Join(errs...)
}

// aaErrors checks aa: true before any I/O. The A/A arm copies arm A and runs
// on the clean condition, so both must exist, and its id is reserved. A
// matrix experiment is refused: the matrix ranks every arm against every
// other, and a copy of arm A would enter that ranking as one more contender.
func aaErrors(e *schemas.Experiment, conds map[string]bool) []error {
	if !e.AA {
		return nil
	}
	var errs []error
	if e.Matrix != nil {
		errs = append(errs, errors.New("aa: true is not supported with a matrix; run the A/A arm in a two-arm experiment of the configs you compare"))
	}
	if _, ok := e.Arms[AAArm]; ok {
		errs = append(errs, fmt.Errorf("arm id %s is reserved for the A/A copy of arm %s when aa: true; rename the declared arm %s", AAArm, aaSourceArm, AAArm))
	}
	if _, ok := e.Arms[aaSourceArm]; !ok {
		errs = append(errs, fmt.Errorf("aa: true needs an arm %s to copy", aaSourceArm))
	}
	if !conds[schemas.ConditionClean] {
		errs = append(errs, fmt.Errorf("aa: true needs the %s condition: the A/A arm runs only there", schemas.ConditionClean))
	}
	return errs
}

// resolveConditions checks each condition against the suite and resolves its
// task selection.
func resolveConditions(suite *schemas.Suite, specs []schemas.ConditionSpec) ([]condPlan, error) {
	seen := map[string]bool{}
	out := make([]condPlan, 0, len(specs))
	for _, c := range specs {
		if seen[c.ID] {
			return nil, fmt.Errorf("condition %s is listed twice", c.ID)
		}
		seen[c.ID] = true
		if c.Trials < 1 {
			return nil, fmt.Errorf("condition %s: trials must be >= 1, got %d", c.ID, c.Trials)
		}
		cp := condPlan{id: c.ID, trials: c.Trials, tasks: c.Tasks.Resolve(suite.TaskIDs)}
		if len(cp.tasks) == 0 {
			return nil, fmt.Errorf("condition %s selects no tasks", c.ID)
		}
		dup := map[string]bool{}
		for _, t := range cp.tasks {
			if dup[t] {
				return nil, fmt.Errorf("condition %s lists task %s twice", c.ID, t)
			}
			dup[t] = true
			if suite.TaskByID(t) == nil {
				return nil, fmt.Errorf("condition %s: task %s is not loaded in suite %s", c.ID, t, suite.ID)
			}
		}
		if c.ID != schemas.ConditionClean {
			if cp.probe = suite.ProbeByID(c.ID); cp.probe == nil {
				return nil, fmt.Errorf("condition %s is neither clean nor a probe of suite %s", c.ID, suite.ID)
			}
		}
		out = append(out, cp)
	}
	return out, nil
}

func compatSpecs(c *schemas.CompatibilitySpec) []schemas.ConditionSpec {
	out := make([]schemas.ConditionSpec, 0, len(c.Conditions))
	for _, id := range c.Conditions {
		out = append(out, schemas.ConditionSpec{ID: id, Tasks: c.Tasks, Trials: c.Trials})
	}
	return out
}

func usedTasks(conds []condPlan) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range conds {
		for _, t := range c.tasks {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	sort.Strings(out)
	return out
}

// drawArmOrder draws one arm order per task from a PCG seeded with the
// experiment seed. Orders are drawn for every suite task in suite order, so a
// task's order does not depend on which other tasks an experiment selects.
func drawArmOrder(seed int64, suiteTasks []string, conds []condPlan, arms []string) map[string][]string {
	tasks, used := drawTasks(suiteTasks, conds)
	r := rand.New(rand.NewPCG(uint64(seed), armOrderStream))
	out := map[string][]string{}
	for _, t := range tasks {
		order := append([]string(nil), arms...)
		r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		if used[t] {
			out[t] = order
		}
	}
	return out
}

// drawAAFirst draws, once per task, whether the A/A arm's slot runs just
// before (true) or just after the A/B pair of each of the task's clean
// blocks, from a PCG seeded with the experiment seed on its own stream, so
// the A/B draws do not move. Drawing per task, as the arm order is, keeps
// the task-level sign flip of A2 against A exact; drawing for every suite
// task in suite order keeps a task's draw independent of the other tasks
// selected. Only tasks that run on clean are returned.
func drawAAFirst(seed int64, suiteTasks []string, conds []condPlan) map[string]bool {
	var clean []condPlan
	for _, c := range conds {
		if c.id == schemas.ConditionClean {
			clean = append(clean, c)
		}
	}
	tasks, used := drawTasks(suiteTasks, clean)
	r := rand.New(rand.NewPCG(uint64(seed), aaOrderStream))
	out := map[string]bool{}
	for _, t := range tasks {
		first := r.IntN(2) == 0
		if used[t] {
			out[t] = first
		}
	}
	return out
}

// drawTasks returns the tasks a per-task draw walks, every suite task in
// suite order and then any used task the suite does not list, and the set of
// tasks conds use.
func drawTasks(suiteTasks []string, conds []condPlan) ([]string, map[string]bool) {
	used := map[string]bool{}
	for _, t := range usedTasks(conds) {
		used[t] = true
	}
	tasks := append([]string(nil), suiteTasks...)
	listed := map[string]bool{}
	for _, t := range tasks {
		listed[t] = true
	}
	for _, t := range usedTasks(conds) {
		if !listed[t] {
			tasks = append(tasks, t)
		}
	}
	return tasks, used
}

// buildSlots lays out every slot. Pairs are interleaved trial by trial, then
// condition by condition, then task by task, and the arms of one pair sit next
// to each other in the task's arm order. A spend-cap stop therefore leaves
// whole trials missing rather than whole conditions, and the two arms of a
// pair run close together in time. Compatibility slots come last.
//
// With aa: true each clean block also gets an A/A slot (arm A2, counted) with
// a pair id of its own, so a pair keeps exactly the arms it had and every
// reader of a pair still sees A against B. It sits right before or right
// after the A/B pair, as drawAAFirst decided for the task; the A/B slots keep
// their order.
func buildSlots(m *schemas.Manifest, suite *schemas.Suite, conds, compat []condPlan) []schemas.Slot {
	var slots []schemas.Slot
	add := func(c condPlan, task, arm, pair string, trial int, counted bool) {
		slots = append(slots, schemas.Slot{
			SlotID:      utils.NewID("s", 10),
			PairID:      pair,
			ExpID:       m.ExpID,
			Suite:       suite.ID,
			Task:        task,
			TaskVersion: suite.TaskByID(task).Version,
			TaskHash:    taskHashFor(m, task, c.id),
			Arm:         arm,
			Condition:   c.id,
			Trial:       trial,
			OrderIndex:  len(slots),
			Counted:     counted,
		})
	}
	var aaFirst map[string]bool
	if m.Experiment.AA {
		aaFirst = drawAAFirst(m.Experiment.Seed, suite.TaskIDs, conds)
	}
	for trial := 1; trial <= maxTrials(conds); trial++ {
		for _, c := range conds {
			if trial > c.trials {
				continue
			}
			for _, task := range c.tasks {
				pair := utils.NewID("p", 10)
				first, aa := aaFirst[task]
				aa = aa && c.id == schemas.ConditionClean
				if aa && first {
					add(c, task, AAArm, utils.NewID("p", 10), trial, true)
				}
				for _, arm := range m.ArmOrder[task] {
					add(c, task, arm, pair, trial, true)
				}
				if aa && !first {
					add(c, task, AAArm, utils.NewID("p", 10), trial, true)
				}
			}
		}
	}
	for trial := 1; trial <= maxTrials(compat); trial++ {
		for _, c := range compat {
			if trial > c.trials {
				continue
			}
			for _, task := range c.tasks {
				add(c, task, CompatArm, utils.NewID("p", 10), trial, false)
			}
		}
	}
	return slots
}

func maxTrials(conds []condPlan) int {
	n := 0
	for _, c := range conds {
		n = max(n, c.trials)
	}
	return n
}

// newExpID returns e_<slug of name>_<4 random chars>. The slug lowercases
// the name and turns every run of other characters into one "_", so the id
// matches the store's ^[a-z0-9_]+$.
func newExpID(name string) string {
	var b strings.Builder
	sep := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			sep = false
			continue
		}
		if !sep && b.Len() > 0 {
			b.WriteByte('_')
			sep = true
		}
	}
	s := strings.TrimRight(b.String(), "_")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "_")
	}
	if s == "" {
		s = "exp"
	}
	return "e_" + s + utils.NewID("", 4)
}

// validID reports whether id matches the store's ^[a-z0-9_]+$.
func validID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
