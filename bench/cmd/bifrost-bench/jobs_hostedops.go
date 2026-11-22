package main

// Hosted container ops bridge (web parity lane L27). agent.check, suite.admit
// and canary.run start containers, so the hosted server never runs them in
// its own process (servejobs.go registerOps). Their hosted versions are small
// experiments run on the organization's runners: the job plans the
// experiment, freezes it with Launcher.Create, queues it with Launcher.Start,
// follows it to the end and returns a verdict summary read from the sealed
// runs. Every bridge experiment answers model calls from the fault proxy's
// scripted model (scripted_upstream), so none of them spends money.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	benchagents "github.com/maximhq/bifrost/bench/agents"
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// Bridge defaults.
const (
	bridgeDefaultTimeout = 30 * time.Minute
	bridgeMaxTimeout     = 6 * time.Hour
	// bridgeIdlePolls is how many polls in a row must see the experiment
	// inactive before it counts as ended: the run job is queued a moment
	// after Start returns.
	bridgeIdlePolls = 2
	bridgeMaxTasks  = 50

	bridgeOracleAgent = "agents/oracle.yaml"
	bridgeNoopAgent   = "agents/noop.yaml"
)

// bridgePollEvery is how often a bridge job polls its experiment (a var so
// tests poll fast).
var bridgePollEvery = 3 * time.Second

// errNoRunner is what a bridge job answers when no runner can take its slots.
var errNoRunner = errors.New("no worker online: hosted checks run as experiments on workers; start a worker (Workers page) or ask your server owner to enable the shared worker")

func init() {
	// The hosted server runs at most four bridge jobs at once (out of its six
	// workers), so the experiments they wait for always find a worker.
	registerJobKind(jobKindSpec{
		Kind: kindAgentCheck, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(2, 1)},
		Validate: validateHostedAgentCheck, Run: runHostedAgentCheck,
	})
	registerJobKind(jobKindSpec{
		Kind: kindSuiteAdmit, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(1, 1)},
		Validate: validateHostedSuiteAdmit, Run: runHostedSuiteAdmit,
	})
	registerOrMergeJobKind(jobKindSpec{
		Kind: kindCanaryRun, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(1, 1)},
		Validate: validateHostedCanaryRun, Run: runHostedCanaryRun,
	})
	// Record baseline on the Drift checks tab queues canary.baseline; the
	// hosted server records it on runners like a look (canary.run with
	// baseline on).
	registerOrMergeJobKind(jobKindSpec{
		Kind: kindCanaryBaseline, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(1, 1)},
		Validate: validateHostedCanaryBaseline, Run: runHostedCanaryBaseline,
	})
}

// Params of the bridge kinds.
type (
	hostedAgentCheckParams struct {
		Agent      string `json:"agent"` // agents/<id>.yaml, relative to the project folder
		Suite      string `json:"suite"` // <id> or <id>@<version>
		Task       string `json:"task,omitempty"`
		TimeoutMin int    `json:"timeout_min,omitempty"`
		// Real is "Try one real task" (Y03, G1 P1-1): the same one-task run
		// on the org's own worker, with the agent's real model calls on the
		// org's key instead of the scripted model, so endpoint agents
		// (HTTP, OpenAI-compatible, A2A) are checked exactly as a run would
		// call them, from where a run would call them.
		Real bool `json:"real,omitempty"`
	}
	hostedSuiteAdmitParams struct {
		Suite      string `json:"suite"`
		Task       string `json:"task,omitempty"` // one task; empty admits every task of the suite
		Oracle     string `json:"oracle,omitempty"`
		Noop       string `json:"noop,omitempty"`
		TimeoutMin int    `json:"timeout_min,omitempty"`
		// JudgeConsent is the organization's yes to the judge calls of a
		// judged benchmark's readiness check (the Build tab toggle): the
		// judge grades the controls on the organization's own key. Without
		// it a judged benchmark's check is refused before any run.
		JudgeConsent bool `json:"judge_consent,omitempty"`
	}
	hostedCanaryParams struct {
		Name       string `json:"name"`
		Baseline   bool   `json:"baseline,omitempty"` // pin a new baseline instead of a look
		Note       string `json:"note,omitempty"`     // baseline note
		Inject     string `json:"inject,omitempty"`   // smoke canaries only: force a probe
		NoConfirm  bool   `json:"no_confirm,omitempty"`
		TimeoutMin int    `json:"timeout_min,omitempty"`
	}
)

// bridgeTimeout turns timeout_min into a duration, refusing nonsense.
func bridgeTimeout(min int) (time.Duration, error) {
	if min < 0 {
		return 0, errors.New("timeout_min must not be negative")
	}
	if min == 0 {
		return bridgeDefaultTimeout, nil
	}
	d := time.Duration(min) * time.Minute
	if d > bridgeMaxTimeout {
		return 0, fmt.Errorf("timeout_min must be at most %d", int(bridgeMaxTimeout/time.Minute))
	}
	return d, nil
}

// validSuiteRef checks "<id>" or "<id>@<version>".
func validSuiteRef(ref string) error {
	id, ver, hasVer := strings.Cut(ref, "@")
	if err := ops.ValidID("suite", id); err != nil {
		return err
	}
	if hasVer {
		if err := ops.ValidID("suite version", ver); err != nil {
			return err
		}
	}
	return nil
}

// validAgentFile checks an agent config path: a .yaml file inside the
// project folder that exists.
// ensureControlAgent writes a scripted control agent (agents/oracle.yaml,
// agents/noop.yaml) into the project from the configs shipped in the binary
// when it is missing. New orgs are not seeded with the controls and the
// agent list hides them from users, but the admission checks still need
// them by path. Any other path, or a file already there, is left alone.
func ensureControlAgent(root, rel string) error {
	if rel != bridgeOracleAgent && rel != bridgeNoopAgent {
		return nil
	}
	b, err := benchagents.Files.ReadFile(filepath.Base(rel))
	if err != nil {
		return fmt.Errorf("control agent %s is not shipped in this build: %w", rel, err)
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
		return err
	}
	// The folder must resolve inside the project (no symlink out of it).
	dir, err := ops.Confine(root, "agent", filepath.Dir(rel))
	if err != nil {
		return err
	}
	p := filepath.Join(dir, filepath.Base(rel))
	if _, err := os.Lstat(p); err == nil {
		return nil
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func validAgentFile(root, what, rel string) error {
	ext := strings.ToLower(filepath.Ext(rel))
	if ext != ".yaml" && ext != ".yml" {
		return fmt.Errorf("%s must be a .yaml agent config, relative to the project folder", what)
	}
	p, err := ops.Confine(root, what, rel)
	if err != nil {
		return err
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return fmt.Errorf("%s %s does not exist in the project; create it on the Agents page (or from a template) first", what, rel)
	}
	return nil
}

func validateHostedAgentCheck(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p hostedAgentCheckParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if err := validAgentFile(env.ProjectRoot, "agent", p.Agent); err != nil {
		return nil, "", invalid(err)
	}
	if err := validSuiteRef(p.Suite); err != nil {
		return nil, "", invalid(err)
	}
	if p.Task != "" {
		if err := ops.ValidID("task", p.Task); err != nil {
			return nil, "", invalid(err)
		}
	}
	if _, err := bridgeTimeout(p.TimeoutMin); err != nil {
		return nil, "", invalid(err)
	}
	return p, "", nil
}

func validateHostedSuiteAdmit(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p hostedSuiteAdmitParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if err := validSuiteRef(p.Suite); err != nil {
		return nil, "", invalid(err)
	}
	if p.Task != "" {
		if err := ops.ValidID("task", p.Task); err != nil {
			return nil, "", invalid(err)
		}
	}
	if p.Oracle == "" {
		p.Oracle = bridgeOracleAgent
	}
	if p.Noop == "" {
		p.Noop = bridgeNoopAgent
	}
	for _, a := range [][2]string{{"oracle", p.Oracle}, {"noop", p.Noop}} {
		if err := ensureControlAgent(env.ProjectRoot, a[1]); err != nil {
			return nil, "", err
		}
		if err := validAgentFile(env.ProjectRoot, a[0], a[1]); err != nil {
			return nil, "", invalid(err)
		}
	}
	if _, err := bridgeTimeout(p.TimeoutMin); err != nil {
		return nil, "", invalid(err)
	}
	id, _, _ := strings.Cut(p.Suite, "@")
	return p, "suite:" + id, nil
}

func validateHostedCanaryRun(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p hostedCanaryParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if !canary.ValidName(p.Name) {
		return nil, "", invalid(fmt.Errorf("canary name %q is not valid", p.Name))
	}
	if _, err := os.Stat(canary.Path(env.ProjectRoot, p.Name)); err != nil {
		return nil, "", invalid(fmt.Errorf("canary %s has no definition (canaries/%s.yaml); create it on the Canaries page first", p.Name, p.Name))
	}
	if p.Inject != "" {
		if p.Baseline {
			return nil, "", invalid(errors.New("inject is for a look, not a baseline"))
		}
		if err := ops.ValidID("inject", p.Inject); err != nil {
			return nil, "", invalid(err)
		}
	}
	if len(p.Note) > 2000 {
		return nil, "", invalid(errors.New("note is longer than 2000 characters"))
	}
	if _, err := bridgeTimeout(p.TimeoutMin); err != nil {
		return nil, "", invalid(err)
	}
	return p, "canary:" + p.Name, nil
}

// validateHostedCanaryBaseline checks canary.baseline's params on the hosted
// server: the local params, without from.
func validateHostedCanaryBaseline(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p canaryBaselineParams
	if err := strictParams(raw, &p); err != nil {
		return nil, "", err
	}
	if p.From != "" {
		return nil, "", invalid(errors.New("Pinning a baseline from an earlier comparison is not available on the hosted site; leave it empty."))
	}
	hp := hostedCanaryParams{Name: p.Name, Baseline: true, Note: p.Note}
	raw2, err := json.Marshal(hp)
	if err != nil {
		return nil, "", err
	}
	if _, _, err := validateHostedCanaryRun(ctx, env, raw2); err != nil {
		return nil, "", err
	}
	return p, "canary:" + p.Name, nil
}

// runHostedCanaryBaseline records a canary's baseline on runners.
func runHostedCanaryBaseline(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p canaryBaselineParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	return runHostedCanary(ctx, env, log, hostedCanaryParams{Name: p.Name, Baseline: true, Note: p.Note})
}

// requireRunner fails fast when the org's launcher has no live runner: a
// hosted experiment would stay queued for ever.
func requireRunner(l handlers.Launcher) error {
	bl := baseLauncher(l)
	if bl == nil || bl.coord == nil {
		return errors.New("this server has no runner coordinator: hosted checks run as experiments on runners")
	}
	for _, w := range bl.coord.Workers() {
		if w.State != schemas.WorkerLost {
			return nil
		}
	}
	return errNoRunner
}

// bridgeLauncher is the org launcher of a bridge job, refusing when none.
func bridgeLauncher(env jobEnv) (handlers.Launcher, error) {
	if env.Services.Launcher == nil {
		return nil, errors.New("this organization has no launcher (the server runs offline); ask your server owner to start it with runners enabled")
	}
	return env.Services.Launcher, nil
}

// runExperiment freezes m, starts it and follows it to the end. A job
// cancelled while it waits cancels the experiment too.
func runExperiment(ctx context.Context, l handlers.Launcher, m *schemas.Manifest, timeout time.Duration, log jobs.Logger) error {
	if err := l.Create(ctx, m); err != nil {
		return fmt.Errorf("freeze %s: %w", m.ExpID, err)
	}
	log.Printf("experiment %s frozen: %d slots", m.ExpID, len(m.Slots))
	if err := l.Start(ctx, m.ExpID); err != nil {
		return fmt.Errorf("start %s: %w", m.ExpID, err)
	}
	log.Printf("experiment %s queued for a runner; following it (timeout %s)", m.ExpID, timeout)
	return waitExperiment(ctx, l, m.ExpID, timeout, bridgePollEvery, log)
}

// expCanceller is implemented by the serve launcher.
type expCanceller interface {
	Cancel(ctx context.Context, expID string) error
}

// waitExperiment logs the experiment's live events and returns once it is
// no longer active (the events channel closed, or the launcher reports it
// inactive for bridgeIdlePolls polls in a row).
func waitExperiment(ctx context.Context, l handlers.Launcher, expID string, timeout, every time.Duration, log jobs.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	events, unsub := l.Events(expID)
	defer unsub()
	act, _ := l.(handlers.ActivityReporter)
	tick := time.NewTicker(every)
	defer tick.Stop()
	idle := 0
	for {
		select {
		case <-ctx.Done():
			if c, ok := l.(expCanceller); ok {
				if err := c.Cancel(context.WithoutCancel(ctx), expID); err != nil {
					log.Warnf("cancel %s: %v", expID, err)
				}
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("experiment %s did not finish within %s; it was cancelled (raise timeout_min, or check the Runners page)", expID, timeout)
			}
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				events = nil // closed: the experiment ended; confirm with the poll
				if act == nil {
					return nil
				}
				continue
			}
			logEvent(log, ev)
		case <-tick.C:
			if act == nil {
				continue
			}
			if act.Active(expID) {
				idle = 0
				continue
			}
			if idle++; idle >= bridgeIdlePolls {
				return nil
			}
		}
	}
}

func logEvent(log jobs.Logger, ev schemas.LiveEvent) {
	switch ev.Kind {
	case "slot_started":
		log.Printf("slot %s started", ev.SlotID)
	case "slot_sealed":
		log.Printf("slot %s sealed: %s", ev.SlotID, ev.Outcome)
	case "error":
		log.Warnf("%s", ev.Message)
	case "stage":
		// too chatty for a job log
	default:
		if ev.Message != "" {
			log.Printf("%s: %s", ev.Kind, ev.Message)
		}
	}
}

// bridgeRun is one sealed run in a bridge summary.
type bridgeRun struct {
	Task      string `json:"task"`
	Arm       string `json:"arm"`
	Condition string `json:"condition"`
	RunID     string `json:"run_id"`
	Outcome   string `json:"outcome"`
	Detail    string `json:"detail,omitempty"`
}

// sealedRuns reads the counted sealed runs of an experiment, in task order.
func sealedRuns(ctx context.Context, env jobEnv, expID string) ([]bridgeRun, error) {
	if env.App == nil || env.App.store == nil {
		return nil, errors.New("no run store")
	}
	srs, err := env.App.store.SealedRuns(context.WithoutCancel(ctx), expID, schemas.GradeSourceOriginal)
	if err != nil {
		return nil, fmt.Errorf("read the runs of %s: %w", expID, err)
	}
	out := make([]bridgeRun, 0, len(srs))
	for _, sr := range srs {
		r := bridgeRun{Task: sr.Slot.Task, Arm: sr.Slot.Arm, Condition: sr.Slot.Condition, RunID: sr.Run.RunID, Outcome: string(sr.Run.Outcome)}
		if len(sr.Run.Failures) > 0 {
			f := sr.Run.Failures[0]
			r.Detail = strings.TrimSpace(f.Part + ": " + f.Error)
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Task != out[j].Task {
			return out[i].Task < out[j].Task
		}
		return out[i].Arm < out[j].Arm
	})
	return out, nil
}

// ---- agent.check ----

// hostedCheckReport is agent.check's hosted result.
type hostedCheckReport struct {
	Agent  string      `json:"agent"`
	Suite  string      `json:"suite"`
	Task   string      `json:"task"`
	ExpID  string      `json:"exp_id"`
	Runs   []bridgeRun `json:"runs"`
	Result string      `json:"result"` // PASS | WARN | FAIL | ERROR
	Detail string      `json:"detail"`
	Real   bool        `json:"real,omitempty"` // a "try one real task" run (real model calls)
}

// checkVerdict grades a hosted agent check from its runs. The scripted model
// solves nothing, so a fail outcome still passes: the check asks whether the
// agent ran, called the gateway and exited by itself.
func checkVerdict(runs []bridgeRun, want int) (string, string) {
	if len(runs) < want {
		return checkError, fmt.Sprintf("%d of %d runs sealed; see the experiment for what stopped it", len(runs), want)
	}
	worst, detail := checkPass, "the agent ran on the runner and exited by itself"
	rank := map[string]int{checkPass: 0, checkWarn: 1, checkFail: 2, checkError: 3}
	for _, r := range runs {
		res, d := checkPass, ""
		switch schemas.Outcome(r.Outcome) {
		case schemas.OutcomePass, schemas.OutcomeFail, schemas.OutcomeRefusal:
		case schemas.OutcomeTimeout, schemas.OutcomeBudget:
			res, d = checkWarn, "the agent did not exit inside its budget ("+r.Outcome+"); check budgets.wall_s and its stop condition"
		case schemas.OutcomeCrash:
			res, d = checkFail, "the agent process failed; open the run's stderr"
		case schemas.OutcomeIntegrityViolation:
			res, d = checkFail, "a veto gate fired (integrity violation); open the run's gates"
		default:
			res, d = checkError, "the run ended "+r.Outcome+": the runner or gateway failed, not the agent; retry, or check the Runners page"
		}
		if r.Detail != "" && res != checkPass {
			d += " (" + r.Detail + ")"
		}
		if rank[res] > rank[worst] {
			worst, detail = res, d
		}
	}
	return worst, detail
}

func runHostedAgentCheck(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p hostedAgentCheckParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	timeout, err := bridgeTimeout(p.TimeoutMin)
	if err != nil {
		return nil, invalid(err)
	}
	l, err := bridgeLauncher(env)
	if err != nil {
		return nil, err
	}
	if err := requireRunner(l); err != nil {
		return nil, err
	}
	task := p.Task
	if task == "" && env.App != nil {
		// Smallest check: the suite's first task.
		s, err := engine.LoadSuite(ctx, env.App.reg, p.Suite)
		if err != nil {
			return nil, fmt.Errorf("suite %s: %w", p.Suite, err)
		}
		if len(s.TaskIDs) == 0 {
			return nil, fmt.Errorf("suite %s has no tasks", p.Suite)
		}
		task = s.TaskIDs[0]
	}
	exp := checkExperiment(p.Agent, p.Suite, task)
	if p.Real {
		exp = realTryExperiment(exp)
	}
	m, err := l.Plan(ctx, exp)
	if err != nil {
		return nil, fmt.Errorf("plan the check: %w", err)
	}
	rep := &hostedCheckReport{Agent: p.Agent, Suite: p.Suite, Task: firstTask(m), ExpID: m.ExpID, Real: p.Real}
	if err := runExperiment(ctx, l, m, timeout, log); err != nil {
		rep.Result, rep.Detail = checkError, err.Error()
		return rep, err
	}
	if rep.Runs, err = sealedRuns(ctx, env, m.ExpID); err != nil {
		return rep, err
	}
	rep.Result, rep.Detail = checkVerdict(rep.Runs, bridgeSlots(m))
	if p.Real && rep.Result == checkPass {
		rep.Detail = "the agent ran one real task on the worker with real model calls and finished by itself"
	}
	for _, r := range rep.Runs {
		log.Printf("%-8s arm %s  %-10s %s", r.Task, r.Arm, r.Outcome, r.RunID)
	}
	log.Printf("agent check %s: %s", rep.Result, rep.Detail)
	if rep.Result == checkFail || rep.Result == checkError {
		return rep, fmt.Errorf("agent check: %s: %s", rep.Result, rep.Detail)
	}
	return rep, nil
}

// checkExperiment is the hosted agent check: the agent as one arm on one
// task (all tasks when task is empty), clean condition, one trial, every
// model call scripted. One arm is scored on its own, so nothing runs twice.
func checkExperiment(agent, suite, task string) *schemas.Experiment {
	tasks := schemas.TaskSelector{All: true}
	if task != "" {
		tasks = schemas.TaskSelector{IDs: []string{task}}
	}
	e := &schemas.Experiment{
		Name:  "check-" + strings.TrimSuffix(strings.TrimSuffix(filepath.Base(agent), ".yaml"), ".yml"),
		Suite: suite,
		Base:  agent,
		Arms: map[string]schemas.ArmSpec{
			"A": {Label: "check"},
		},
		Primary:          schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:           []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions:       []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: tasks, Trials: 1}},
		Verdict:          schemas.VerdictSpec{Policy: "paired_signflip", Alpha: 0.05, AssumedSD: 0.30},
		Seed:             1,
		Concurrency:      2,
		ManifestMode:     "agent_exposure",
		Smoke:            true,
		ScriptedUpstream: true,
	}
	return e
}

// realTryExperiment turns the check into "Try one real task": one trial
// of one task with the agent's real model calls (no smoke, no scripted
// model). It runs on the org's worker like any evaluation, so an endpoint
// agent is called from the same place and with the same gateway, tools and
// retries as in a run. It is its own experiment, so it never changes
// another evaluation's results.
func realTryExperiment(e *schemas.Experiment) *schemas.Experiment {
	out := *e
	out.Name = "try-" + strings.TrimPrefix(e.Name, "check-")
	out.Arms = map[string]schemas.ArmSpec{"A": {Label: "try"}}
	out.Smoke, out.ScriptedUpstream = false, false
	out.Concurrency = 1
	return &out
}

func firstTask(m *schemas.Manifest) string {
	if len(m.Slots) == 0 {
		return ""
	}
	return m.Slots[0].Task
}

func bridgeSlots(m *schemas.Manifest) int {
	n := 0
	for _, s := range m.Slots {
		if s.Counted && !s.Optional {
			n++
		}
	}
	return n
}

// ---- suite.admit ----

// hostedAdmitTask is one task's readiness result in the hosted summary.
type hostedAdmitTask struct {
	Task     string `json:"task"`
	Oracle   string `json:"oracle"` // outcome of the first reference run (want pass)
	Noop     string `json:"noop"`   // outcome of the noop control (want fail)
	Admitted bool   `json:"admitted"`
	Reason   string `json:"reason,omitempty"`
	// Checks are the task's admission checks, named as local admission
	// names them (jobs_hostedreadiness.go); the record in suite.lock holds
	// them after static_validation.
	Checks           []schemas.AdmissionCheck `json:"checks,omitempty"`
	ReferenceResults []string                 `json:"-"`
}

// hostedAdmitReport is suite.admit's hosted result.
type hostedAdmitReport struct {
	Suite    string            `json:"suite"`
	ExpID    string            `json:"exp_id"`
	ExpIDs   []string          `json:"exp_ids,omitempty"` // one per split (public, held out)
	Tasks    []hostedAdmitTask `json:"tasks"`
	Admitted int               `json:"admitted"`
	Failed   int               `json:"failed"`
	LockPath string            `json:"lock,omitempty"`
	Dropped  []string          `json:"dropped,omitempty"`
	Judge    *readinessJudge   `json:"judge,omitempty"`
	Note     string            `json:"note"`
}

// Arms A and B of the readiness experiment: the first reference run and
// the noop control (the paired verdict needs both ids).
const admitArmOracle, admitArmNoop = "A", "B"

// errJudgeConsent refuses a judged benchmark's readiness check without the
// organization's consent to the judge calls.
func errJudgeConsent(j readinessJudge) error {
	return invalid(fmt.Errorf("the readiness checks of this benchmark call your judge model %s/%s about %d times on your own key: agree to the judge calls in the Build tab (judge_consent) and run them again", j.Provider, j.Model, j.Calls))
}

func runHostedSuiteAdmit(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p hostedSuiteAdmitParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	if _, err := bridgeTimeout(p.TimeoutMin); err != nil {
		return nil, invalid(err)
	}
	if p.Oracle == "" {
		p.Oracle = bridgeOracleAgent
	}
	if p.Noop == "" {
		p.Noop = bridgeNoopAgent
	}
	l, err := bridgeLauncher(env)
	if err != nil {
		return nil, err
	}
	if err := bridgeRequireRunner(l); err != nil {
		return nil, err
	}
	if env.App == nil || env.App.cfg == nil {
		return nil, errors.New("no organization app")
	}
	// As local `suite admit`: static validation first; any error refuses the
	// admission before a runner is used and nothing is written.
	suiteID, _, _ := strings.Cut(p.Suite, "@")
	st, err := snapshotSuite(ctx, env.App, suiteID)
	if err != nil {
		return nil, err
	}
	if p.Task != "" && st.suite.TaskByID(p.Task) == nil {
		return nil, fmt.Errorf("suite %s has no task %q in its tasks list", suiteID, p.Task)
	}
	if errs := staticErrors(ctx, st.suite, p.Task); len(errs) > 0 {
		for _, e := range errs {
			log.Warnf("error   %s", e)
		}
		return nil, fmt.Errorf("static validation found %d errors; fix them (Edit tab) and admit again", len(errs))
	}
	var tasks []*schemas.Task
	for _, t := range st.suite.Tasks {
		if p.Task == "" || t.ID == p.Task {
			tasks = append(tasks, t)
		}
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("suite %s has no task to check", suiteID)
	}
	if len(tasks) > bridgeMaxTasks {
		return nil, fmt.Errorf("suite %s has %d tasks; admit at most %d at once (name one task)", p.Suite, len(tasks), bridgeMaxTasks)
	}
	judge := readinessJudgeOf(st.suite, tasks)
	if judge.Judged && !p.JudgeConsent {
		return nil, errJudgeConsent(judge)
	}
	rep := &hostedAdmitReport{Suite: p.Suite,
		Note: "hosted readiness checks run every control on a worker (the right answer three times, the empty answer, each near miss and wrong answer, a gaming attempt, doing nothing and refusing) and record each result in suite.lock"}
	if judge.Judged {
		rep.Judge = &judge
		log.Printf("the judge %s/%s grades the controls on your own key: about %d calls", judge.Provider, judge.Model, judge.Calls)
	}
	runs, expOf, err := runReadiness(ctx, env, l, p, st.suite, tasks, judge.Judged, rep, log, progress)
	if err != nil {
		return rep, err
	}
	rep.Tasks = readinessVerdict(st.suite, tasks, runs, expOf, readinessFileChecks(env.App, st.suite))
	path, dropped, err := recordHostedAdmissions(ctx, env, st, rep.Tasks, rep.ExpID, jobActor(j))
	if err != nil {
		return rep, fmt.Errorf("record the admission: %w", err)
	}
	rep.LockPath, rep.Dropped = path, dropped
	log.Printf("wrote %s", path)
	for _, id := range dropped {
		log.Warnf("the admission of %s was not recorded: the task or suite changed during the run; admit it again", id)
	}
	for _, t := range rep.Tasks {
		if t.Admitted {
			rep.Admitted++
			log.Printf("ADMIT   %s (%d checks passed)", t.Task, len(t.Checks))
		} else {
			rep.Failed++
			log.Warnf("REFUSE  %s: %s", t.Task, t.Reason)
		}
	}
	if progress != nil {
		progress(map[string]any{"done": len(rep.Tasks), "total": len(tasks), "exp_id": rep.ExpID, "admitted": rep.Admitted, "refused": rep.Failed})
	}
	if rep.Failed > 0 {
		return rep, fmt.Errorf("suite %s: %d tasks not admitted", p.Suite, rep.Failed)
	}
	return rep, nil
}

// runReadiness plans and runs the readiness experiment of tasks, one per
// split, and returns the sealed runs of all of them with the experiment
// each task ran in. rep gets the experiment ids as they are planned.
func runReadiness(ctx context.Context, env jobEnv, l handlers.Launcher, p hostedSuiteAdmitParams, s *schemas.Suite, tasks []*schemas.Task, paidJudge bool,
	rep *hostedAdmitReport, log jobs.Logger, progress func(any)) ([]bridgeRun, map[string]string, error) {
	var all []bridgeRun
	expOf := map[string]string{}
	for _, group := range readinessGroups(tasks) {
		split := schemas.SplitPublic
		if group[0].Split == schemas.SplitHeldOut {
			split = schemas.SplitHeldOut
		}
		m, err := l.Plan(ctx, readinessExperiment(p.Suite, p.Oracle, p.Noop, s, group, split, paidJudge))
		if err != nil {
			return all, expOf, fmt.Errorf("plan the readiness run: %w", err)
		}
		if rep.ExpID == "" {
			rep.ExpID = m.ExpID
		}
		rep.ExpIDs = append(rep.ExpIDs, m.ExpID)
		for _, t := range group {
			expOf[t.ID] = m.ExpID
		}
		timeout := readinessTimeout(len(m.Slots))
		if p.TimeoutMin > 0 {
			timeout, _ = bridgeTimeout(p.TimeoutMin)
		}
		log.Printf("readiness controls of %d %s tasks run in experiment %s (open it on the Experiments page)", len(group), splitWord(split), m.ExpID)
		if progress != nil {
			progress(map[string]any{"done": 0, "total": len(tasks), "exp_id": m.ExpID})
		}
		if err := runExperiment(ctx, l, m, timeout, log); err != nil {
			return all, expOf, err
		}
		runs, err := bridgeSealedRuns(ctx, env, m.ExpID)
		if err != nil {
			return all, expOf, err
		}
		all = append(all, runs...)
	}
	return all, expOf, nil
}

func splitWord(split string) string {
	if split == schemas.SplitHeldOut {
		return "held-out"
	}
	return "public"
}

// readinessFileChecks runs suite.FileChecks with the org's registered
// grader of s when there is one.
func readinessFileChecks(a *app, s *schemas.Suite) func(*schemas.Task) []schemas.AdmissionCheck {
	var g schemas.Grader
	if a != nil && a.reg != nil {
		for _, x := range a.reg.Graders {
			if x != nil && x.GetName() == s.Grader {
				g = x
				break
			}
		}
	}
	return func(t *schemas.Task) []schemas.AdmissionCheck { return suite.FileChecks(s, t, g) }
}

// ---- canary.run ----

// hostedCanaryExecutor is the canary executor of the hosted server: the
// engine executor's planning and read-back, with the experiment run on
// runners through the launcher.
type hostedCanaryExecutor struct {
	canaryExecutor
	l       handlers.Launcher
	timeout time.Duration
	log     jobs.Logger
}

// Execute implements canary.Executor.
func (x hostedCanaryExecutor) Execute(ctx context.Context, p *canary.Plan) (string, float64, error) {
	m, ok := p.Manifest.(*schemas.Manifest)
	if !ok || m == nil {
		return "", 0, errors.New("canary plan carries no manifest")
	}
	if err := requireRunner(x.l); err != nil {
		return "", 0, err
	}
	runErr := runExperiment(ctx, x.l, m, x.timeout, x.log)
	res := &schedule.Result{ExpID: m.ExpID}
	scheduleRunner{a: x.a}.tally(ctx, res)
	if runErr != nil {
		return m.ExpID, res.CostUSD, fmt.Errorf("run %s: %w", m.ExpID, runErr)
	}
	return m.ExpID, res.CostUSD, nil
}

// hostedCanaryResult is canary.run's hosted result.
type hostedCanaryResult struct {
	Canary  string          `json:"canary"`
	Records []canary.Record `json:"records"`
	Outcome string          `json:"outcome"`
	State   string          `json:"state"`
	ExpIDs  []string        `json:"exp_ids"`
}

func runHostedCanaryRun(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p hostedCanaryParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	return runHostedCanary(ctx, env, log, p)
}

// noBaselineMessage is the error of a look at a canary with no baseline.
func noBaselineMessage(name string) string {
	return "Drift monitor " + name + " has no baseline yet: press Record baseline on Scheduled checks > Drift checks first."
}

// runHostedCanary runs one hosted look, or records a baseline when
// p.Baseline, on the org's runners.
func runHostedCanary(ctx context.Context, env jobEnv, log jobs.Logger, p hostedCanaryParams) (any, error) {
	timeout, err := bridgeTimeout(p.TimeoutMin)
	if err != nil {
		return nil, invalid(err)
	}
	if env.App == nil {
		return nil, errors.New("no organization app")
	}
	l, err := bridgeLauncher(env)
	if err != nil {
		return nil, err
	}
	if err := requireRunner(l); err != nil {
		return nil, err
	}
	d, err := canary.Load(canary.Path(env.ProjectRoot, p.Name))
	if err != nil {
		return nil, fmt.Errorf("canary %s: %w", p.Name, err)
	}
	r := newCanaryRunner(env.App)
	r.Exec = hostedCanaryExecutor{canaryExecutor: canaryExecutor{a: env.App}, l: l, timeout: timeout, log: log}
	out := &hostedCanaryResult{Canary: d.Name}
	if p.Baseline {
		rec, err := r.Baseline(ctx, d, canary.BaselineOptions{Note: p.Note})
		if rec != nil {
			out.Records = []canary.Record{*rec}
		}
		finishCanaryResult(out, log)
		return out, err
	}
	recs, err := r.Run(ctx, d, canary.RunOptions{Inject: p.Inject, NoConfirm: p.NoConfirm})
	if errors.Is(err, canary.ErrNoBaseline) {
		return nil, errors.New(noBaselineMessage(d.Name))
	}
	out.Records = recs
	finishCanaryResult(out, log)
	return out, err
}

func finishCanaryResult(out *hostedCanaryResult, log jobs.Logger) {
	for _, rec := range out.Records {
		if rec.ExpID != "" {
			out.ExpIDs = append(out.ExpIDs, rec.ExpID)
		}
		log.Printf("%s look: %s, state %s%s", rec.Kind, rec.Outcome, rec.State, reasonSuffix(rec.Reason))
		out.Outcome, out.State = rec.Outcome, rec.State
	}
}

func reasonSuffix(r string) string {
	if r == "" {
		return ""
	}
	return " (" + r + ")"
}
