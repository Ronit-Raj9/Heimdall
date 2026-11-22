package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Web parity lane L14: experiment.extend, experiment.migrate and probe.run.
// Each writes a follow-up experiment file under experiments/, plans it,
// freezes it and starts it through the org's Launcher, so in hosted mode the
// runs go to the org's runners like any other experiment (nothing here starts
// a container in the server process).

const (
	kindExperimentExtend  = "experiment.extend"
	kindExperimentMigrate = "experiment.migrate"
	kindProbeRun          = "probe.run"

	maxFollowUpTrials = 50
	maxFollowUpTasks  = 500
	maxMigrateModels  = 6
)

type (
	extendJobParams struct {
		ExpID   string `json:"exp_id"`
		Tasks   int    `json:"tasks,omitempty"`  // run N tasks the experiment did not run
		Trials  int    `json:"trials,omitempty"` // run K more trials of its tasks
		Force   bool   `json:"force,omitempty"`  // extend a verdict that is not INCONCLUSIVE
		NoStart bool   `json:"no_start,omitempty"`
	}
	migrateJobParams struct {
		Agent         string   `json:"agent"` // agents/<id>.yaml, relative to the project folder
		From          string   `json:"from"`
		To            []string `json:"to"`
		Suite         string   `json:"suite"` // id@version
		Provider      string   `json:"provider,omitempty"`
		Trials        int      `json:"trials,omitempty"` // default 3
		Conditions    []string `json:"conditions,omitempty"`
		Tasks         []string `json:"tasks,omitempty"`
		BudgetUSD     float64  `json:"budget_usd,omitempty"`
		Policy        string   `json:"policy,omitempty"` // default policies/default.yaml when it exists
		AllowUnpriced bool     `json:"allow_unpriced,omitempty"`
		NoStart       bool     `json:"no_start,omitempty"`
	}
	probeJobParams struct {
		ExpID  string   `json:"exp_id"`
		Probes []string `json:"probes,omitempty"` // default (no probes and no faults): every probe of the suite
		// Faults are fault catalog names (faults.Names, such as model-brownout),
		// mapped to conditions on the benchmark by faults.Conditions, so a
		// benchmark without probes of its own still gets a fault test (fix
		// wave 2, N65).
		Faults  []string `json:"faults,omitempty"`
		Trials  int      `json:"trials,omitempty"` // default: the clean condition's trials
		NoStart bool     `json:"no_start,omitempty"`
	}
)

// followUpResult is what each of the three kinds returns.
type followUpResult struct {
	ExpID   string   `json:"exp_id"`
	Parent  string   `json:"parent,omitempty"`
	File    string   `json:"file"`
	Slots   int      `json:"slots"`
	Started bool     `json:"started"`
	Notes   []string `json:"notes,omitempty"`
}

var modelRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,127}$`)

func init() {
	registerJobKind(jobKindSpec{
		Kind: kindExperimentExtend, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithMaxAttempts(1)},
		Validate: validateExtendJob,
		Run:      runExtendJob,
	})
	registerJobKind(jobKindSpec{
		Kind: kindExperimentMigrate, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithMaxAttempts(1)},
		Validate: validateMigrateJob,
		Run:      runMigrateJob,
	})
	registerJobKind(jobKindSpec{
		Kind: kindProbeRun, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithMaxAttempts(1)},
		Validate: validateProbeJob,
		Run:      runProbeJob,
	})
}

// validateParentExp checks expID names a frozen experiment of the org.
func validateParentExp(ctx context.Context, env jobEnv, expID string) (*schemas.Manifest, error) {
	if err := ops.ValidID("exp_id", expID); err != nil {
		return nil, invalid(err)
	}
	m, err := env.App.store.LoadManifest(ctx, expID)
	if err != nil {
		return nil, fmt.Errorf("%w: experiment %s: %v", jobs.ErrNotFound, expID, err)
	}
	return m, nil
}

func validateExtendJob(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p extendJobParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if (p.Tasks > 0) == (p.Trials > 0) {
		return nil, "", invalid(errors.New("set exactly one of tasks (new tasks to run) or trials (more trials of the same tasks), with a value of 1 or more"))
	}
	if p.Tasks > maxFollowUpTasks || p.Trials > maxFollowUpTrials || p.Tasks < 0 || p.Trials < 0 {
		return nil, "", invalid(fmt.Errorf("tasks must be at most %d and trials at most %d", maxFollowUpTasks, maxFollowUpTrials))
	}
	if _, err := validateParentExp(ctx, env, p.ExpID); err != nil {
		return nil, "", err
	}
	return p, expTarget(p.ExpID), nil
}

func runExtendJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p extendJobParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	a := env.App
	m, err := a.store.LoadManifest(ctx, p.ExpID)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", p.ExpID, err)
	}
	progress(map[string]any{"phase": "analyzing", "exp_id": p.ExpID})
	an, _, err := a.analyze(ctx, p.ExpID, "")
	if err != nil {
		return nil, fmt.Errorf("analyze %s: %w", p.ExpID, err)
	}
	if an.Verdict.Decision != schemas.DecisionInconclusive && !p.Force {
		return nil, fmt.Errorf("%s is %s, not INCONCLUSIVE: there is nothing to buy more evidence for (tick \"extend anyway\" to run it regardless)", p.ExpID, an.Verdict.Decision)
	}
	s, err := engine.LoadSuite(ctx, a.reg, m.Experiment.Suite)
	if err != nil {
		return nil, err
	}
	kind, n := engine.ExtendTasks, p.Tasks
	if p.Trials > 0 {
		kind, n = engine.ExtendTrials, p.Trials
	}
	e, notes, err := engine.ExtendExperiment(m, s, kind, n)
	if err != nil {
		return nil, err
	}
	log.Printf("follow-up of %s: %s %d, combination rule %s", p.ExpID, kind, n, e.Extends.Rule)
	if me := an.Verdict.MoreEvidence; me != nil {
		log.Printf("sizing: %s", sizingText(me))
	}
	for _, note := range notes {
		log.Printf("note: %s", note)
	}
	return launchFollowUp(ctx, env, e, p.ExpID, notes, p.NoStart, log, progress)
}

func validateProbeJob(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p probeJobParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if p.Trials < 0 || p.Trials > maxFollowUpTrials {
		return nil, "", invalid(fmt.Errorf("trials must be between 1 and %d", maxFollowUpTrials))
	}
	for _, id := range p.Probes {
		if err := ops.ValidID("probe", id); err != nil {
			return nil, "", invalid(err)
		}
	}
	for _, f := range p.Faults {
		if !slices.Contains(faults.Names, f) {
			return nil, "", invalid(fmt.Errorf("unknown fault %q; pick one from the list", f))
		}
	}
	if _, err := validateParentExp(ctx, env, p.ExpID); err != nil {
		return nil, "", err
	}
	return p, expTarget(p.ExpID), nil
}

// probeWanted resolves what a probe job asks for on s: want is the suite
// probes it names plus the conditions of the catalog faults it names, and
// allowed is every id the follow-up may run (the suite's own probes plus those
// fault conditions). A fault that cannot run on s is in skipped. With faults
// asked and none of them able to run (and no probe named), it is an error.
func probeWanted(s *schemas.Suite, probes, faultNames []string) (want, allowed []string, skipped []faults.Skip, err error) {
	allowed = slices.Clone(s.ProbeIDs)
	want = slices.Clone(probes)
	if len(faultNames) == 0 {
		return want, allowed, nil, nil
	}
	conds, skipped := faults.Conditions(faultNames, s)
	for _, id := range conds[1:] {
		if !slices.Contains(allowed, id) {
			allowed = append(allowed, id)
		}
		if !slices.Contains(want, id) {
			want = append(want, id)
		}
	}
	if len(want) == 0 {
		reasons := make([]string, 0, len(skipped))
		for _, sk := range skipped {
			reasons = append(reasons, sk.Fault+": "+sk.Reason)
		}
		return nil, nil, skipped, fmt.Errorf("none of the chosen faults can run on benchmark %s: %s", s.ID, strings.Join(reasons, "; "))
	}
	return want, allowed, skipped, nil
}

// probeExperiment is the parent's experiment with its clean condition and
// one condition per probe on the same tasks, as a fresh fixed design.
func probeExperiment(m *schemas.Manifest, suiteProbes []string, want []string, trials int) (*schemas.Experiment, []string, error) {
	probes := want
	if len(probes) == 0 {
		probes = suiteProbes
	}
	if len(probes) == 0 {
		return nil, nil, errors.New("the suite declares no probes; add probes to its suite.yaml first")
	}
	for _, id := range probes {
		if !slices.Contains(suiteProbes, id) {
			return nil, nil, fmt.Errorf("probe %q is not one of the suite's probes (%s)", id, strings.Join(suiteProbes, ", "))
		}
	}
	pe := m.Experiment
	e := pe
	e.ID = ""
	e.Name = pe.Name + "-probes"
	e.Seed = pe.Seed + 1
	e.Matrix = nil
	e.Compatibility = nil
	e.Extends = nil
	e.Trials = nil
	base := schemas.ConditionSpec{ID: schemas.ConditionClean, Tasks: schemas.TaskSelector{All: true}, Trials: 1}
	for _, c := range pe.Conditions {
		if c.ID == schemas.ConditionClean {
			base = c
			break
		}
	}
	if len(pe.Conditions) > 0 && base.ID != schemas.ConditionClean {
		base = pe.Conditions[0]
		base.ID = schemas.ConditionClean
	}
	if trials > 0 {
		base.Trials = trials
	}
	var notes []string
	conds := []schemas.ConditionSpec{base}
	for _, id := range probes {
		if id == schemas.ConditionClean {
			continue
		}
		c := base
		c.ID = id
		conds = append(conds, c)
	}
	e.Conditions = conds
	e.Primary.Condition = schemas.ConditionClean
	e.Arms = cloneArms(pe.Arms)
	notes = append(notes, fmt.Sprintf("conditions: clean + %s, %d trial(s) each", strings.Join(probes, ", "), base.Trials))
	return &e, notes, nil
}

func cloneArms(in map[string]schemas.ArmSpec) map[string]schemas.ArmSpec {
	out := make(map[string]schemas.ArmSpec, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func runProbeJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p probeJobParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	a := env.App
	m, err := a.store.LoadManifest(ctx, p.ExpID)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", p.ExpID, err)
	}
	s, err := engine.LoadSuite(ctx, a.reg, m.Experiment.Suite)
	if err != nil {
		return nil, err
	}
	want, allowed, skipped, err := probeWanted(s, p.Probes, p.Faults)
	if err != nil {
		return nil, err
	}
	e, notes, err := probeExperiment(m, allowed, want, p.Trials)
	if err != nil {
		return nil, err
	}
	for _, sk := range skipped {
		notes = append(notes, fmt.Sprintf("fault %s does not apply to %s: %s", sk.Fault, s.ID, sk.Reason))
	}
	for _, n := range notes {
		log.Printf("%s", n)
	}
	return launchFollowUp(ctx, env, e, p.ExpID, notes, p.NoStart, log, progress)
}

func validateMigrateJob(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p migrateJobParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if p.Trials == 0 {
		p.Trials = 3
	}
	if p.Trials < 1 || p.Trials > maxFollowUpTrials {
		return nil, "", invalid(fmt.Errorf("trials must be between 1 and %d", maxFollowUpTrials))
	}
	if p.Agent == "" || p.From == "" || len(p.To) == 0 || p.Suite == "" {
		return nil, "", invalid(errors.New("agent, from, to and suite are required"))
	}
	if len(p.To) > maxMigrateModels {
		return nil, "", invalid(fmt.Errorf("at most %d target models per migration", maxMigrateModels))
	}
	if !strings.HasSuffix(p.Agent, ".yaml") && !strings.HasSuffix(p.Agent, ".yml") {
		return nil, "", invalid(errors.New("agent must be an agent config file such as agents/<id>.yaml"))
	}
	if _, err := ops.Confine(env.App.cfg.Root, "agent", p.Agent); err != nil {
		return nil, "", invalid(err)
	}
	if p.Policy != "" && p.Policy != "none" {
		if _, err := ops.Confine(env.App.cfg.Root, "policy", p.Policy); err != nil {
			return nil, "", invalid(err)
		}
	}
	for _, mdl := range append([]string{p.From, p.Suite}, p.To...) {
		if !modelRefRE.MatchString(mdl) {
			return nil, "", invalid(fmt.Errorf("%q: use letters, digits and . _ : / @ + - only", mdl))
		}
	}
	if p.Provider != "" && !modelRefRE.MatchString(p.Provider) {
		return nil, "", invalid(fmt.Errorf("provider %q is not a provider id", p.Provider))
	}
	for _, id := range append(slices.Clone(p.Conditions), p.Tasks...) {
		if err := ops.ValidID("condition or task", id); err != nil {
			return nil, "", invalid(err)
		}
	}
	if p.BudgetUSD < 0 {
		return nil, "", invalid(errors.New("budget_usd cannot be negative"))
	}
	if slices.Contains(p.To, p.From) {
		return nil, "", invalid(errors.New("the target models must differ from the current model"))
	}
	return p, "migrate:" + slug(p.From+"-to-"+strings.Join(p.To, "-")), nil
}

func runMigrateJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p migrateJobParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	if p.Trials == 0 {
		p.Trials = 3
	}
	a := env.App
	policyFile := p.Policy
	if policyFile == "" {
		if _, err := os.Stat(a.cfg.Path("policies/default.yaml")); err == nil {
			policyFile = "policies/default.yaml"
		}
	}
	text := migrationExperiment(p.Agent, p.Suite, p.From, p.To, p.Provider, p.Conditions, p.Tasks, p.Trials, p.BudgetUSD, 0, policyFile, p.AllowUnpriced)
	tmp, err := os.CreateTemp("", "heimdall-migrate-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()
	e, err := loadExperiment(tmp.Name())
	if err != nil {
		return nil, fmt.Errorf("migration experiment: %w", err)
	}
	e.Name = slug("migrate-" + p.From + "-to-" + strings.Join(p.To, "-"))
	log.Printf("migration of %s from %s to %s on %s, %d trial(s)", p.Agent, p.From, strings.Join(p.To, ", "), p.Suite, p.Trials)
	return launchFollowUp(ctx, env, e, "", nil, p.NoStart, log, progress)
}

// launchFollowUp validates the policy, plans, writes the experiment file,
// freezes the manifest and starts it.
func launchFollowUp(ctx context.Context, env jobEnv, e *schemas.Experiment, parent string, notes []string, noStart bool, log jobs.Logger, progress func(any)) (any, error) {
	a := env.App
	l := env.Services.Launcher
	if l == nil {
		return nil, errors.New("this server has no experiment launcher; start it with a database (serve) to run experiments from the web")
	}
	if err := a.validatePolicy(e); err != nil {
		return nil, fmt.Errorf("policy %s: %w", e.Policy, err)
	}
	progress(map[string]any{"phase": "planning", "parent": parent})
	m, err := l.Plan(ctx, e)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	slots := countedSlots(m)
	log.Printf("planned %s: %d counted runs over %d arm(s)", m.ExpID, slots, len(m.Experiment.Arms))
	if f, err := a.forecast(ctx, m); err == nil && f != nil {
		log.Printf("forecast: %s %s", f.Fit, f.FitReason)
	}
	file, err := writeFollowUpFile(a.cfg.Path("experiments"), e)
	if err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(a.cfg.Root, file)
	log.Printf("wrote %s", rel)
	if err := l.Create(ctx, m); err != nil {
		if errors.Is(err, schemas.ErrExists) {
			return nil, fmt.Errorf("experiment %s already exists: open it from the experiments list, or change its name in %s", m.ExpID, rel)
		}
		return nil, fmt.Errorf("freeze %s: %w", m.ExpID, err)
	}
	res := followUpResult{ExpID: m.ExpID, Parent: parent, File: rel, Slots: slots, Notes: notes}
	if !noStart {
		if err := l.Start(ctx, m.ExpID); err != nil {
			return nil, fmt.Errorf("start %s: %w (the experiment is frozen; start it from its page)", m.ExpID, err)
		}
		res.Started = true
		log.Printf("started %s", m.ExpID)
	}
	progress(map[string]any{"phase": "done", "exp_id": m.ExpID})
	return res, nil
}

// writeFollowUpFile writes the experiment YAML under dir, never over an
// existing file.
func writeFollowUpFile(dir string, e *schemas.Experiment) (string, error) {
	raw, err := experimentYAML(e)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := slug(e.Name)
	if name == "" {
		name = "experiment"
	}
	for i := 0; i < 20; i++ {
		base := name
		if i > 0 {
			base = fmt.Sprintf("%s-%s-%d", name, time.Now().UTC().Format("20060102t150405"), i)
		}
		path := filepath.Join(dir, base+".yaml")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(raw)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return path, werr
	}
	return "", fmt.Errorf("could not pick a free file name for %s under %s", name, dir)
}
