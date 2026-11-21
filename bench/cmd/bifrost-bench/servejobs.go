package main

// Jobs area (P4): serve's experiment launcher. Each started experiment is an
// experiment.run job of the job runner (bench/jobs): the job runs the
// scheduler in resume mode, so a cancel stops it (in-flight runs seal as
// aborted_infra and never count) and a restarted server resumes it. The live
// event stream of /experiments/{id}/events is fed from the job's handler.
// Without a job runner (no database) the launcher runs experiments in this
// process as before, without cancel or resume.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// Job kinds of the experiments area.
const (
	kindExperimentRun    = "experiment.run"
	kindExperimentRegate = "experiment.regate"
)

// expTarget is the job target of an experiment's jobs.
func expTarget(expID string) string { return "exp:" + expID }

var (
	_ handlers.Launcher         = (*launcher)(nil)
	_ handlers.ActivityReporter = (*launcher)(nil)
	_ handlers.RunController    = (*launcher)(nil)
)

// launcher plans, freezes and starts experiments for the API and fans live
// events out to subscribers.
type launcher struct {
	a  *app
	ix *store.Index

	org        string              // organization of this launcher's experiments
	jobs       *jobs.Runner        // nil: run in-process, no cancel or resume
	hub        *jobsHub            // the hub that holds it (hosted), for Detach
	coord      *engine.Coordinator // Remote area: opens runs to remote workers (worker.go)
	remoteOnly bool                // Runners area: run no slot locally (hosted mode)

	mu     sync.Mutex
	active map[string]*liveRun

	stateMu  sync.Mutex
	states   map[string]jobs.Status // newest experiment.run job status per target
	statesAt time.Time
}

// liveRun is one running experiment's event fan-out.
type liveRun struct {
	job  string // the run job feeding it; "" until a job is bound (guarded by launcher.mu)
	mu   sync.Mutex
	seq  int
	subs map[chan schemas.LiveEvent]bool
	done bool
}

func newLauncher(a *app, ix *store.Index) *launcher {
	return &launcher{a: a, ix: ix, org: auth.LocalOrgID, active: map[string]*liveRun{}}
}

func (l *launcher) Plan(ctx context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	m, err := l.a.newPlanner().Plan(ctx, exp)
	if err == nil {
		err = l.keyBudgetCheck(ctx, m) // G10: a provider with only over-budget keys (keybudget.go)
	}
	return m, err
}

func (l *launcher) Create(ctx context.Context, m *schemas.Manifest) error {
	if err := l.a.store.CreateExperiment(ctx, m); err != nil {
		return err
	}
	return l.rebuild(ctx)
}

func (l *launcher) rebuild(ctx context.Context) error {
	if l.ix == nil {
		return nil
	}
	err := l.ix.Rebuild(ctx)
	if err != nil {
		l.a.logger.Warn("index rebuild failed", "error", err.Error())
	}
	return err
}

// Active reports whether an experiment is running: its newest experiment.run
// job is queued or running, or it runs in this process.
func (l *launcher) Active(expID string) bool {
	if l.jobs == nil {
		l.mu.Lock()
		defer l.mu.Unlock()
		r, ok := l.active[expID]
		return ok && !r.isDone()
	}
	st, ok := l.jobState(expID)
	return ok && (st == jobs.StatusQueued || st == jobs.StatusRunning)
}

// Cancelled reports whether the experiment's newest run job was cancelled.
func (l *launcher) Cancelled(expID string) bool {
	st, ok := l.jobState(expID)
	return ok && st == jobs.StatusCancelled
}

// jobState reads the newest experiment.run status of expID from a cache the
// list page shares, refreshed every second at most.
func (l *launcher) jobState(expID string) (jobs.Status, bool) {
	if l.jobs == nil {
		return "", false
	}
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	if l.states == nil || time.Since(l.statesAt) > time.Second {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		st, err := l.jobs.LatestByTarget(ctx, l.org, kindExperimentRun)
		if err != nil {
			l.a.logger.Warn("read experiment jobs", "error", err.Error())
			return "", false
		}
		l.states, l.statesAt = st, time.Now()
	}
	st, ok := l.states[expTarget(expID)]
	return st, ok
}

func (l *launcher) invalidateStates() {
	l.stateMu.Lock()
	l.states = nil
	l.stateMu.Unlock()
}

// Start queues the experiment's run job (or, with no job runner, runs it in
// this process). An experiment whose run job is queued or running is refused.
func (l *launcher) Start(ctx context.Context, expID string) error {
	if m, err := l.a.store.LoadManifest(ctx, expID); err == nil {
		if err := l.keyBudgetCheck(ctx, m); err != nil { // G10 (keybudget.go)
			return err
		}
	}
	if l.jobs == nil {
		return l.startInProcess(ctx, expID)
	}
	if _, err := l.a.store.LoadManifest(ctx, expID); err != nil {
		return err
	}
	params, _ := json.Marshal(expRunParams{ExpID: expID})
	j, err := l.jobs.Enqueue(ctx, jobs.NewJob{OrgID: l.org, Kind: kindExperimentRun, Params: params, CreatedBy: actorOf(ctx)})
	if errors.Is(err, jobs.ErrConflict) {
		return fmt.Errorf("experiment %s already has a queued or running job (a run or a regrade): %w", expID, schemas.ErrExists)
	}
	if err != nil {
		return err
	}
	l.invalidateStates()
	l.liveRunFor(expID, j.ID) // subscribers may follow before the job is claimed
	return nil
}

// Resume queues the run job of an experiment that stopped (cancelled, failed,
// or interrupted with no job left): the scheduler resumes from its sealed
// runs. It is Start under another name, refused while a run job is active.
func (l *launcher) Resume(ctx context.Context, expID string) error {
	if l.jobs == nil {
		return errors.New("resume needs the job runner (serve opens a database for it)")
	}
	return l.Start(ctx, expID)
}

// Cancel requests the cancellation of the experiment's active run job: no
// new slot is leased, in-flight runs are stopped and sealed as aborted_infra
// (never counted), and the experiment's state becomes cancelled.
func (l *launcher) Cancel(ctx context.Context, expID string) error {
	if l.jobs == nil {
		return errors.New("cancel needs the job runner (serve opens a database for it)")
	}
	j, err := l.jobs.Latest(ctx, l.org, kindExperimentRun, expTarget(expID))
	if errors.Is(err, jobs.ErrNotFound) {
		return fmt.Errorf("experiment %s is not running: %w", expID, handlers.ErrNotRunning)
	}
	if err != nil {
		return err
	}
	c, err := l.jobs.Cancel(ctx, l.org, j.ID)
	if err != nil {
		if errors.Is(err, jobs.ErrFinished) {
			return fmt.Errorf("experiment %s is not running: %w", expID, handlers.ErrNotRunning)
		}
		return err
	}
	l.invalidateStates()
	if c.Status == jobs.StatusCancelled { // cancelled before it started: no handler closes the fan-out
		l.finishLive(expID, c.ID)
	}
	return nil
}

// finishLive closes the experiment's fan-out, if it has one: its job ended
// without a handler that would (cancelled while queued). jobID names that
// job: a fan-out already bound to another (the next run job, started after
// this one was cancelled) is left open. "" closes whatever is open.
func (l *launcher) finishLive(expID, jobID string) {
	l.mu.Lock()
	r, ok := l.active[expID]
	if ok && jobID != "" && r.job != "" && r.job != jobID {
		ok = false
	}
	l.mu.Unlock()
	if ok {
		r.finish()
	}
}

// startInProcess runs the experiment detached from any request in this
// process: the launcher before the job runner, kept for a server without a
// database.
func (l *launcher) startInProcess(ctx context.Context, expID string) error {
	m, err := l.a.store.LoadManifest(ctx, expID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	if r, ok := l.active[expID]; ok && !r.isDone() {
		l.mu.Unlock()
		return fmt.Errorf("experiment %s is already running", expID)
	}
	run := &liveRun{subs: map[chan schemas.LiveEvent]bool{}}
	l.active[expID] = run
	l.mu.Unlock()
	go func() {
		res, err := l.execute(context.Background(), m, run, nil, nil)
		run.finish()
		notifyInProcessFinished(l.org, expID, res, err) // gap G13: experiment.finished webhooks with no job runner
	}()
	return nil
}

// liveRunFor returns the experiment's open fan-out for job jobID, opening a
// new one when there is none, the last one finished, or the open one is
// bound to another job. An unbound fan-out is bound to jobID; jobID "" takes
// whatever is open.
func (l *launcher) liveRunFor(expID, jobID string) *liveRun {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r, ok := l.active[expID]; ok && !r.isDone() && (jobID == "" || r.job == "" || r.job == jobID) {
		if r.job == "" {
			r.job = jobID
		}
		return r
	}
	r := &liveRun{job: jobID, subs: map[chan schemas.LiveEvent]bool{}}
	l.active[expID] = r
	return r
}

// expRunParams are the params of experiment.run.
type expRunParams struct {
	ExpID string `json:"exp_id"`
}

// expRunProgress is experiment.run's progress: counts so far and the last
// live event.
type expRunProgress struct {
	ExpID     string             `json:"exp_id"`
	Phase     string             `json:"phase"` // running | analyzing | stopping
	Slots     int                `json:"slots"`
	Sealed    int                `json:"sealed"`
	Skipped   int                `json:"skipped"`
	Errors    int                `json:"errors"`
	LastEvent *schemas.LiveEvent `json:"last_event,omitempty"`
}

// expRunResult is experiment.run's result.
type expRunResult struct {
	ExpID      string          `json:"exp_id"`
	Summary    *engine.Summary `json:"summary,omitempty"`
	Decision   string          `json:"decision,omitempty"`
	AnalysisID string          `json:"analysis_id,omitempty"`
}

// runJob is the experiment.run handler for this launcher's organization.
func (l *launcher) runJob(ctx context.Context, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p expRunParams
	if err := j.DecodeParams(&p); err != nil || p.ExpID == "" {
		return nil, fmt.Errorf("experiment.run params: want {\"exp_id\": ...}")
	}
	m, err := l.a.store.LoadManifest(ctx, p.ExpID)
	if err != nil {
		return nil, err
	}
	if j.Resume() {
		log.Printf("resuming experiment %s (attempt %d): sealed runs are kept, interrupted runs are sealed, the rest run", m.ExpID, j.Attempt)
	} else {
		log.Printf("running experiment %s: %d slots", m.ExpID, len(m.Slots))
	}
	run := l.liveRunFor(m.ExpID, j.ID)
	defer run.finish()
	return l.execute(ctx, m, run, log, progress)
}

// execute runs the scheduler in resume mode (Create already froze the
// manifest), forwards its events to the live fan-out, the job log and the
// job progress, then analyzes. When ctx ends (a cancel, a shutdown) the
// scheduler leases nothing more and its in-flight runs are stopped with
// engine.ErrRunCancelled, so they seal as aborted_infra; the analysis is
// skipped and ctx's cause is returned. log and progress may be nil.
func (l *launcher) execute(ctx context.Context, m *schemas.Manifest, run *liveRun, log jobs.Logger, progress func(any)) (any, error) {
	if log == nil {
		log = discardLog{}
	}
	if progress == nil {
		progress = func(any) {}
	}
	prog := expRunProgress{ExpID: m.ExpID, Phase: "running", Slots: len(m.Slots)}
	progress(prog)
	events := make(chan engine.Event, 256)
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for ev := range events {
			le := schemas.LiveEvent{Kind: ev.Kind, SlotID: ev.SlotID, RunID: ev.RunID, Stage: ev.Stage, Outcome: ev.Outcome, Message: ev.Message, At: ev.At}
			run.publish(le)
			switch ev.Kind {
			case engine.EventRunSealed:
				prog.Sealed++
				log.Printf("sealed %s (slot %s): %s", ev.RunID, ev.SlotID, ev.Outcome)
				_ = l.rebuild(context.WithoutCancel(ctx))
				evidenceOnSeal(l.org, m.ExpID) // Evidence storage area (P6), wired by Tenancy: mirror the sealed run now (evidence.go)
				l.chargeKeySpend(ctx, m, ev.RunID, log) // G10: the run's cost to its model key (keybudget.go)
			case engine.EventRunRecovered:
				log.Warnf("sealed interrupted run %s (slot %s): %s", ev.RunID, ev.SlotID, ev.Outcome)
				evidenceOnSeal(l.org, m.ExpID) // Evidence storage area (P6), wired by Tenancy (evidence.go)
				l.chargeKeySpend(ctx, m, ev.RunID, log) // G10 (keybudget.go)
			case engine.EventSlotSkipped:
				prog.Skipped++
			case engine.EventError:
				prog.Errors++
				log.Errorf("slot %s: %s", ev.SlotID, ev.Message)
			case engine.EventBudgetStop, engine.EventDone:
				log.Printf("%s: %s", ev.Kind, ev.Message)
			case engine.EventStage, engine.EventRunStarted:
				// live view only
			default:
				log.Printf("%s %s %s", ev.Kind, ev.SlotID, ev.Message)
			}
			last := le
			prog.LastEvent = &last
			progress(prog)
		}
	}()

	// The scheduler's context ends with ErrRunCancelled when the job's does,
	// so a run it stops is sealed as aborted_infra (engine/cancel.go).
	rctx, stopRun := engine.CancelContext(ctx)
	defer stopRun()

	l.applyLocalOrgKeys(ctx, m, log) // G10: local serve delivers Models page keys to its gateway (keybudget.go)
	janitor := engine.NewJanitor(l.a.reg.Gateway, l.a.docker, l.a.logger)
	sched := engine.NewScheduler(l.a.reg, l.a.cfg.Root, l.a.logger, janitor)
	sum, err := sched.Run(rctx, m, engine.RunOptions{Resume: true, Events: events, Remote: l.coord, RemoteOnly: l.remoteOnly})
	close(events)
	<-forwarded
	res := expRunResult{ExpID: m.ExpID, Summary: sum}
	if cause := context.Cause(ctx); cause != nil {
		prog.Phase = "stopping"
		progress(prog)
		msg := "experiment stopped: " + cause.Error()
		if errors.Is(cause, jobs.ErrCancelled) {
			msg = "experiment cancelled: in-flight runs were stopped and sealed as aborted_infra (never counted); resume runs the rest"
		}
		run.publish(schemas.LiveEvent{Kind: "cancelled", Message: msg, At: time.Now()})
		log.Warnf("%s", msg)
		_ = l.rebuild(context.Background())
		return res, cause
	}
	if err != nil {
		run.publish(schemas.LiveEvent{Kind: "error", Message: err.Error(), At: time.Now()})
		log.Errorf("scheduler: %v", err)
	}
	prog.Phase = "analyzing"
	progress(prog)
	actx := context.WithoutCancel(ctx)
	an, _, aerr := l.a.analyze(actx, m.ExpID, "")
	if aerr != nil {
		run.publish(schemas.LiveEvent{Kind: "error", Message: "analyze: " + aerr.Error(), At: time.Now()})
		log.Errorf("analyze: %v", aerr)
	} else {
		res.Decision, res.AnalysisID = string(an.Verdict.Decision), an.AnalysisID
		run.publish(schemas.LiveEvent{Kind: "experiment_done", Message: string(an.Verdict.Decision), At: time.Now()})
		log.Printf("analysis %s: %s", an.AnalysisID, an.Verdict.Decision)
	}
	_ = l.rebuild(actx)
	if err != nil {
		return res, err
	}
	return res, aerr
}

func (l *launcher) Events(expID string) (<-chan schemas.LiveEvent, func()) {
	l.mu.Lock()
	run, ok := l.active[expID]
	l.mu.Unlock()
	if (!ok || run.isDone()) && l.Active(expID) {
		// A queued or resumed job: open the fan-out its handler will use.
		run, ok = l.liveRunFor(expID, ""), true
	}
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

// isDone reports whether finish ran, under the run's own mutex (finish
// writes done under it, not under the launcher's).
func (r *liveRun) isDone() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
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

// discardLog is a jobs.Logger that drops everything (in-process runs).
type discardLog struct{}

func (discardLog) Printf(string, ...any)       {}
func (discardLog) Warnf(string, ...any)        {}
func (discardLog) Errorf(string, ...any)       {}
func (discardLog) Write(p []byte) (int, error) { return len(p), nil }

// actorOf is the created_by of a job a request starts: the principal's actor
// id (user, token or runner id; "local" in local mode), else "system".
func actorOf(ctx context.Context) string {
	if p, ok := auth.Lookup(ctx); ok {
		if id := p.ActorID(); id != "" {
			return id
		}
	}
	return "system"
}

// Busy reports an experiment running in this process, so a hosted org's
// runtime is not evicted under it (tenancy.OrgRuntime.Busy). With a job
// runner the job's status decides, not the optional live fan-out: a fan-out
// left open for a job that already ended is closed here instead.
func (l *launcher) Busy() bool {
	l.mu.Lock()
	var open []string
	for id, r := range l.active {
		if !r.isDone() {
			open = append(open, id)
		}
	}
	l.mu.Unlock()
	busy := false
	for _, id := range open {
		if l.jobs == nil || l.Active(id) {
			busy = true
			continue
		}
		l.finishLive(id, "")
	}
	return busy
}

// Regrade queues an experiment.regate job: the gates of the experiment's
// sealed runs are recomputed as a new regrade, whose id is in the job's
// params (generated here when req has none).
func (l *launcher) Regrade(ctx context.Context, expID string, req handlers.RegradeRequest) (*jobs.Job, error) {
	if l.jobs == nil {
		return nil, errors.New("regrade needs the job runner (serve opens a database for it)")
	}
	params, err := json.Marshal(regateParams{ExpID: expID, ID: req.ID, Note: req.Note, SuiteLock: req.SuiteLock})
	if err != nil {
		return nil, err
	}
	j, err := l.jobs.Enqueue(ctx, jobs.NewJob{OrgID: l.org, Kind: kindExperimentRegate, Params: params, CreatedBy: actorOf(ctx)})
	if err == nil {
		l.invalidateStates()
	}
	return j, err
}

// Job kinds of the ops area (bench/ops).
const (
	kindSuiteLint    = "suite.lint"
	kindSuiteLock    = "suite.lock"
	kindSuiteAdmit   = "suite.admit"
	kindAgentCheck   = "agent.check"
	kindIndexRebuild = "index.rebuild"
)

// expRunMaxAttempts: an experiment run resumes after every restart; its
// scheduler is idempotent, so it gets more claims than the default.
const expRunMaxAttempts = 20

// Hosted job runner sizing. A hosted experiment.run waits in its scheduler
// until a matching runner takes its slots, possibly for ever, so experiments
// get at most hostedExpLimit workers (hostedExpPerOrg per organization) and
// the rest are left to the ops kinds and to other organizations.
const (
	hostedWorkers   = 6
	hostedExpLimit  = 4
	hostedExpPerOrg = 2
)

// jobsHub is serve's job runner and the way a job finds its organization's
// launcher: the one local launcher, or in hosted mode the launcher the
// org's runtime registered when the tenancy registry built it.
type jobsHub struct {
	runner     *jobs.Runner
	db         *db.DB
	hosted     bool
	launches   bool   // experiment.run is registered (not -offline)
	configPath string // serve's -config, for agent.check's own app

	mu    sync.Mutex
	orgs  map[string]*launcher
	build func(ctx context.Context, orgID string) (value any, release func(), err error) // hosted: acquire the org's runtime and its bound launcher
}

func newJobsHub(d *db.DB, logger schemas.Logger, hosted, launches bool, configPath string) *jobsHub {
	opts := jobs.Options{Logger: logger}
	if hosted {
		opts.Workers = hostedWorkers
	}
	h := &jobsHub{runner: jobs.New(d, opts), db: d, hosted: hosted, launches: launches, configPath: configPath,
		orgs: map[string]*launcher{}}
	h.registerKinds()
	return h
}

// add registers an organization's launcher and wires it to the runner.
func (h *jobsHub) add(l *launcher) {
	l.jobs, l.hub = h.runner, h
	h.mu.Lock()
	h.orgs[l.org] = l
	h.mu.Unlock()
}

// Detach takes the launcher out of its hub when its org runtime closes.
func (l *launcher) Detach() {
	if l.hub != nil {
		l.hub.remove(l)
	}
}

// remove drops l when it is still the organization's launcher: the org's
// runtime was evicted, and the hub must not keep its index and app alive
// (4-B-12). A newer runtime's launcher stays.
func (h *jobsHub) remove(l *launcher) {
	h.mu.Lock()
	if h.orgs[l.org] == l {
		delete(h.orgs, l.org)
	}
	h.mu.Unlock()
}

// launcherFor returns the organization's launcher, building its runtime in
// hosted mode; release ends the hold on that runtime.
func (h *jobsHub) launcherFor(ctx context.Context, orgID string) (*launcher, func(), error) {
	release := func() {}
	var bound *launcher
	if h.build != nil {
		v, rel, err := h.build(ctx, orgID)
		if err != nil {
			return nil, nil, fmt.Errorf("organization %s: %w", orgID, err)
		}
		release = rel
		bound, _ = v.(*launcher)
	}
	// The launcher of the runtime this job holds, so the job and the
	// requests of that runtime share one launcher and index (audit
	// qc2-17-03); the hub lookup serves local mode.
	l, ok := bound, bound != nil
	if !ok {
		h.mu.Lock()
		l, ok = h.orgs[orgID]
		h.mu.Unlock()
	}
	if !ok {
		release()
		return nil, nil, fmt.Errorf("%w: organization %s has no launcher on this server (offline?)", jobs.ErrInvalidParams, orgID)
	}
	return l, release, nil
}

// withLauncher wraps a handler that needs the job's organization.
func (h *jobsHub) withLauncher(f func(ctx context.Context, l *launcher, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error)) jobs.Handler {
	return func(ctx context.Context, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
		l, release, err := h.launcherFor(ctx, j.OrgID)
		if err != nil {
			return nil, err
		}
		defer release()
		return f(ctx, l, j, log, progress)
	}
}

// strictParams decodes params into v, refusing unknown fields.
func strictParams(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
	}
	return nil
}

func invalid(err error) error { return fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err) }

// Params of the kinds.
type (
	suiteParams struct {
		Suite          string `json:"suite"`
		Task           string `json:"task,omitempty"`             // suite.admit: one task
		PinLocalImages *bool  `json:"pin_local_images,omitempty"` // suite.lock
	}
	agentCheckParams struct {
		Agent   string `json:"agent"` // agents/<id>.yaml, relative to the project folder
		Suite   string `json:"suite"`
		Task    string `json:"task,omitempty"`
		NoBuild bool   `json:"no_build,omitempty"`
	}
	regateParams struct {
		ExpID     string `json:"exp_id"`
		ID        string `json:"id,omitempty"`         // regrade id; generated when empty
		Note      string `json:"note,omitempty"`       // why
		SuiteLock string `json:"suite_lock,omitempty"` // frozen suite.lock, relative to the project folder
	}
	emptyParams struct{}
)

// validateExp checks an experiment id names a frozen experiment of the org.
func validateExp(ctx context.Context, l *launcher, expID string) error {
	if err := ops.ValidID("exp_id", expID); err != nil {
		return invalid(err)
	}
	if _, err := l.a.store.LoadManifest(ctx, expID); err != nil {
		return fmt.Errorf("%w: experiment %s: %v", jobs.ErrNotFound, expID, err)
	}
	return nil
}

// validateSuite checks a suite id names a folder of the org's benchmarks.
func validateSuite(l *launcher, id string) error {
	if err := ops.ValidID("suite", id); err != nil {
		return invalid(err)
	}
	if _, err := ops.Confine(l.a.cfg.Path(l.a.cfg.BenchmarksRoot), "suite", id); err != nil {
		return invalid(err)
	}
	return nil
}

// validator builds a jobs.Validator that resolves the org's launcher first.
func (h *jobsHub) validator(f func(ctx context.Context, l *launcher, raw json.RawMessage) (any, string, error)) jobs.Validator {
	return func(ctx context.Context, orgID string, raw json.RawMessage) (any, string, error) {
		l, release, err := h.launcherFor(ctx, orgID)
		if err != nil {
			return nil, "", err
		}
		defer release()
		return f(ctx, l, raw)
	}
}

// registerKinds registers every kind serve runs. suite.admit and agent.check
// start containers from the project's suites and agents, so they exist in
// local mode only: strangers' code never runs on the hosted server.
func (h *jobsHub) registerKinds() {
	if h.launches { // an -offline server never claims an experiment run; a queued one waits for a launching server
		h.registerExperimentRun()
	}
	h.registerOps()
	h.registerJobKinds() // web parity kinds (jobkinds.go); after registerOps so its early return cannot skip them
}

// registerExperimentRun registers experiment.run.
func (h *jobsHub) registerExperimentRun() {
	opts := []jobs.KindOption{jobs.Internal(), jobs.WithMaxAttempts(expRunMaxAttempts), jobs.WithLimit(0, 0), jobs.WithCancelHook(h.expRunCancelled)}
	if h.hosted {
		// A hosted experiment only waits in its scheduler until a runner
		// takes its slots, so it is claimed only for an organization with a
		// live runner: experiments of organizations without one stay queued
		// and never hold the experiment workers an eligible one needs.
		opts = append(opts, jobs.WithLimit(hostedExpLimit, hostedExpPerOrg), jobs.WithClaimGate(h.orgsWithRunners))
	}
	h.runner.Register(kindExperimentRun, h.withLauncher(func(ctx context.Context, l *launcher, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
		return l.runJob(ctx, j, log, progress)
	}), append(opts, jobs.WithValidator(h.validator(
		func(ctx context.Context, l *launcher, raw json.RawMessage) (any, string, error) {
			var p expRunParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			if err := validateExp(ctx, l, p.ExpID); err != nil {
				return nil, "", err
			}
			if l.a.cfg.Auth.IsHosted() && l.coord == nil { // rule 4: hosted experiments run on runners only
				return nil, "", invalid(errors.New("hosted experiments run on runners; this server has no runner coordinator"))
			}
			return p, expTarget(p.ExpID), nil
		})))...)
}

// orgsWithRunners lists the organizations whose launcher has a runner
// coordinator with at least one registered runner that is not lost.
func (h *jobsHub) orgsWithRunners() []string {
	h.mu.Lock()
	ls := make([]*launcher, 0, len(h.orgs))
	for _, l := range h.orgs {
		ls = append(ls, l)
	}
	h.mu.Unlock()
	var out []string
	for _, l := range ls {
		if l.coord == nil {
			continue
		}
		for _, w := range l.coord.Workers() {
			if w.State != schemas.WorkerLost {
				out = append(out, l.org)
				break
			}
		}
	}
	return out
}

// expRunCancelled closes the live fan-out of an experiment whose run job
// was cancelled while queued, through any API.
func (h *jobsHub) expRunCancelled(j *jobs.Job) {
	var p expRunParams
	if err := j.DecodeParams(&p); err != nil || p.ExpID == "" {
		return
	}
	h.mu.Lock()
	l, ok := h.orgs[j.OrgID]
	h.mu.Unlock()
	if ok {
		l.invalidateStates()
		l.finishLive(p.ExpID, j.ID)
	}
}

// registerOps registers experiment.regate and the ops kinds.
func (h *jobsHub) registerOps() {
	r := h.runner
	r.Register(kindExperimentRegate, h.withLauncher(regateJob), jobs.WithValidator(h.validator(
		func(ctx context.Context, l *launcher, raw json.RawMessage) (any, string, error) {
			var p regateParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			if err := validateExp(ctx, l, p.ExpID); err != nil {
				return nil, "", err
			}
			if p.ID == "" {
				p.ID = utils.NewID("rg", 8)
			}
			if err := ops.ValidID("id", p.ID); err != nil {
				return nil, "", invalid(err)
			}
			if p.ID == schemas.GradeSourceOriginal || p.ID == schemas.GradeSourceReviewed {
				return nil, "", invalid(fmt.Errorf("id %q is reserved", p.ID))
			}
			if len(p.Note) > 2000 {
				return nil, "", invalid(errors.New("note is longer than 2000 characters"))
			}
			if p.SuiteLock != "" {
				if _, err := ops.Confine(l.a.cfg.Root, "suite_lock", p.SuiteLock); err != nil {
					return nil, "", invalid(err)
				}
			}
			return p, expTarget(p.ExpID), nil
		})))

	suiteValidator := func(needTask bool) jobs.Validator {
		return h.validator(func(ctx context.Context, l *launcher, raw json.RawMessage) (any, string, error) {
			var p suiteParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			if err := validateSuite(l, p.Suite); err != nil {
				return nil, "", err
			}
			if p.Task != "" {
				if !needTask {
					return nil, "", invalid(errors.New("task is only for suite.admit"))
				}
				if err := ops.ValidID("task", p.Task); err != nil {
					return nil, "", invalid(err)
				}
			}
			return p, "suite:" + p.Suite, nil
		})
	}
	lintValidator := h.validator(func(ctx context.Context, l *launcher, raw json.RawMessage) (any, string, error) {
		var p suiteParams
		if err := strictParams(raw, &p); err != nil {
			return nil, "", err
		}
		if p.Task != "" || p.PinLocalImages != nil {
			return nil, "", invalid(errors.New("suite.lint takes only suite"))
		}
		if err := validateSuite(l, p.Suite); err != nil {
			return nil, "", err
		}
		return p, "", nil // read-only: lints may run beside anything
	})
	r.Register(kindSuiteLint, h.withLauncher(suiteLintJob), jobs.WithValidator(lintValidator))
	r.Register(kindSuiteLock, h.withLauncher(suiteLockJob), jobs.WithValidator(suiteValidator(false)))
	r.Register(kindIndexRebuild, h.withLauncher(h.indexRebuildJob), jobs.WithValidator(h.validator(
		func(ctx context.Context, l *launcher, raw json.RawMessage) (any, string, error) {
			var p emptyParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			return p, "index", nil
		})))
	if h.hosted || !h.launches {
		// An -offline server launches nothing: suite.admit and agent.check
		// start containers and reach the gateway, so they are not
		// registered and a queued one waits for a launching server.
		return
	}
	r.Register(kindSuiteAdmit, h.withLauncher(suiteAdmitJob), jobs.WithValidator(suiteValidator(true)))
	r.Register(kindAgentCheck, h.withLauncher(h.agentCheckJob), jobs.WithValidator(h.validator(
		func(ctx context.Context, l *launcher, raw json.RawMessage) (any, string, error) {
			var p agentCheckParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			ext := strings.ToLower(filepath.Ext(p.Agent))
			if ext != ".yaml" && ext != ".yml" {
				return nil, "", invalid(errors.New("agent must be a .yaml agent config, relative to the project folder"))
			}
			if _, err := ops.Confine(l.a.cfg.Root, "agent", p.Agent); err != nil {
				return nil, "", invalid(err)
			}
			if err := ops.ValidID("suite", p.Suite); err != nil {
				return nil, "", invalid(err)
			}
			if p.Task != "" {
				if err := ops.ValidID("task", p.Task); err != nil {
					return nil, "", invalid(err)
				}
			}
			return p, "", nil
		})))
}

func suiteLintJob(ctx context.Context, l *launcher, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p suiteParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	rep, err := ops.SuiteLint(ctx, l.a.cfg.Path(l.a.cfg.BenchmarksRoot), p.Suite, log)
	if err != nil {
		return nil, err
	}
	if rep.Errors > 0 {
		return rep, fmt.Errorf("suite %s: %d lint errors", rep.Suite, rep.Errors)
	}
	return rep, nil
}

func suiteLockJob(ctx context.Context, l *launcher, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p suiteParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	return ops.SuiteLock(ctx, ops.LockOptions{BenchRoot: l.a.cfg.Path(l.a.cfg.BenchmarksRoot), Ref: p.Suite, Config: l.a.cfg,
		Docker: l.a.docker, Pin: p.PinLocalImages}, log)
}

func suiteAdmitJob(ctx context.Context, l *launcher, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p suiteParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	env := ops.AdmitEnv{Config: l.a.cfg, Docker: l.a.docker, Logger: l.a.logger,
		Registry: func(context.Context, bool) (*schemas.Registry, error) { return l.a.reg, nil }} // serve's app already has the gateway
	rep, err := ops.SuiteAdmit(ctx, env, p.Suite, p.Task, log)
	if err != nil {
		return rep, err
	}
	if rep.Failed > 0 {
		return rep, fmt.Errorf("suite %s: %d tasks not admitted", rep.Suite, rep.Failed)
	}
	return rep, nil
}

func (h *jobsHub) agentCheckJob(ctx context.Context, l *launcher, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p agentCheckParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	agent, err := ops.Confine(l.a.cfg.Root, "agent", p.Agent) // resolved again right before use
	if err != nil {
		return nil, err
	}
	rep := agentCheck(ctx, checkOptions{config: h.configPath, agent: agent, suite: p.Suite, task: p.Task,
		runs: defaultChecksRoot, noBuild: p.NoBuild}, log)
	printCheckReport(log, rep)
	if code := checkReportExit(rep); code != exitOK {
		return rep, fmt.Errorf("agent check: %s", rep.Result)
	}
	return rep, nil
}

func (h *jobsHub) indexRebuildJob(ctx context.Context, l *launcher, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	extra := []func(context.Context) error{func(ctx context.Context) error { return l.rebuild(ctx) }}
	if h.db != nil {
		extra = append(extra, func(ctx context.Context) error {
			_, err := h.db.RebuildOrg(ctx, l.org, l.a.store) // Database area (P1): the idx_* tables
			return err
		})
	}
	ix, st, err := ops.IndexRebuild(ctx, l.a.store, l.a.logger, extra...)
	if err != nil {
		return nil, err
	}
	printIndexStatus(log, ix, st)
	return st, nil
}

// regateResult is experiment.regate's result.
type regateResult struct {
	ExpID     string `json:"exp_id"`
	RegradeID string `json:"regrade_id"`
	Runs      int    `json:"runs"`
	Changed   int    `json:"changed"` // runs whose outcome the regate changed
}

func regateJob(ctx context.Context, l *launcher, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p regateParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	lock := ""
	if p.SuiteLock != "" {
		var err error
		if lock, err = ops.Confine(l.a.cfg.Root, "suite_lock", p.SuiteLock); err != nil {
			return nil, err
		}
	}
	info, before, after, err := ops.Regate(ctx, l.a.reg, l.a.store, l.a.logger, p.ExpID, p.ID, p.Note, lock)
	if err != nil {
		return nil, fmt.Errorf("regate %s: %w", p.ExpID, err)
	}
	fmt.Fprint(log, gateSourceTables(before, after, info.GateVersions))
	if info.SuiteLockHash != "" {
		fmt.Fprintf(log, "\n%s\n", suiteDriftText(info.SuiteLockHash, before))
	}
	fmt.Fprintf(log, "\nregrade %s written to %s/regrades/%s\n", p.ID, p.ExpID, p.ID)
	res := regateResult{ExpID: p.ExpID, RegradeID: p.ID, Runs: len(after)}
	was := map[string]schemas.Outcome{}
	for _, r := range before {
		was[r.Run.RunID] = r.Run.Outcome
	}
	for _, r := range after {
		if o, ok := was[r.Run.RunID]; ok && o != r.Run.Outcome {
			res.Changed++
		}
	}
	_ = l.rebuild(context.WithoutCancel(ctx))
	return res, nil
}

// startLocalJobs opens local mode's database (SQLite under the runs root by
// default), migrates it, and starts the job runner with the local
// launcher: experiments then run as jobs with cancel and resume, and the ops
// kinds are on /jobs. Without a database serve keeps working, with the
// in-process launcher and no job API. The returned stop waits for running
// jobs to hand back (they resume at the next start) and closes the database.
func startLocalJobs(ctx context.Context, a *app, configPath string, l handlers.Launcher, ix *store.Index, deps *server.Deps) func() {
	d, err := db.Open(ctx, a.cfg.DatabaseConfig())
	if err == nil {
		if err = d.Migrate(ctx); err != nil {
			d.Close()
		}
	}
	if err != nil {
		a.logger.Warn("job runner off (experiments run in-process, without cancel or resume): database: " + err.Error())
		return func() {}
	}
	hub := newJobsHub(d, a.logger, false, l != nil, configPath)
	base := baseLauncher(l)
	if base == nil {
		// Offline: no experiments, but the ops kinds still run over the
		// project; a launcher that never starts anything carries the app.
		base = newLauncher(a, ix)
	}
	base.org = auth.LocalOrgID
	hub.add(base)
	if err := hub.runner.Start(context.WithoutCancel(ctx)); err != nil {
		a.logger.Warn("job runner: " + err.Error())
	}
	deps.Jobs = hub.runner
	a.logger.Info("job runner on: " + dbDescription(d))
	return func() {
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		hub.runner.Stop(sctx)
		d.Close()
	}
}

// baseLauncher returns the job launcher behind a handlers.Launcher.
func baseLauncher(l handlers.Launcher) *launcher {
	switch v := l.(type) {
	case *launcher:
		return v
	case *workerLauncher:
		return v.launcher
	}
	return nil
}

func dbDescription(d *db.DB) string {
	if d.Driver() == db.SQLite {
		return "sqlite " + d.Path()
	}
	return d.Driver()
}

// Hosted mode (Tenancy area hooks, servetenancy.go): one job runner over the
// hosted database serves every organization; each org runtime registers its
// launcher, and a job for an org whose runtime is not built yet (a resume
// after a restart) builds it through the registry.
var hostedHubs sync.Map // *hostedEnv -> *jobsHub

func hostedHub(env *hostedEnv) *jobsHub {
	if h, ok := hostedHubs.Load(env); ok {
		return h.(*jobsHub)
	}
	// Hosted -offline keeps experiment.run: the hosted coordinator queues
	// leases for runners and launches nothing itself, so offline changes
	// nothing here, as the startup warning says (audit 8-C-03, 4-B-11).
	h := newJobsHub(env.db, env.base.logger, true, true, "")
	h.build = func(ctx context.Context, orgID string) (any, func(), error) {
		org, err := env.dir.OrgByID(ctx, orgID)
		if err != nil {
			return nil, nil, err
		}
		rt, release, err := env.registry.Acquire(ctx, org)
		if err != nil {
			return nil, nil, err
		}
		return rt.Value, release, nil
	}
	actual, loaded := hostedHubs.LoadOrStore(env, h)
	if loaded {
		return actual.(*jobsHub)
	}
	if err := h.runner.Start(context.WithoutCancel(env.ctx)); err != nil {
		env.base.logger.Warn("job runner: " + err.Error())
	}
	context.AfterFunc(env.ctx, func() { stopHostedHub(env) })
	return h
}

// stopHostedHub stops env's job runner and waits for its running jobs to
// hand back. serveHosted calls it before closing the org runtimes and the
// database the hand-back writes to; a repeat call waits for the first.
func stopHostedHub(env *hostedEnv) {
	h, ok := hostedHubs.Load(env)
	if !ok {
		return
	}
	sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h.(*jobsHub).runner.Stop(sctx)
}

// jobsAudit writes the job routes' audit rows (rule 6): platform handlers
// write their own.
func jobsAudit(env *hostedEnv) handlers.AuditFunc {
	return func(ctx *fasthttp.RequestCtx, orgID, action, targetType, targetID string, details map[string]any) {
		if env.audit == nil {
			return
		}
		_ = env.audit.Audit(context.WithoutCancel(ctx), tenancy.EntryFor(ctx, orgID, action, targetType, targetID, details))
	}
}

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		return handlers.NewJobsHandler(env.base.cfg.Server.Mode, hostedHub(env).runner, nil).WithAudit(jobsAudit(env)), nil
	})
	// Replaces the runners area's default (runnerhosted.go): the same
	// launcher, remote only, with the job runner attached.
	orgLauncher = func(env *hostedEnv, oa *app, ix *store.Index) (handlers.Launcher, handlers.WorkerAPI) {
		l := newLauncher(oa, ix)
		l.org = oa.orgID
		l.remoteOnly = true // rule 4: strangers' agents never run on the hosted server
		var api handlers.WorkerAPI
		if c, err := hostedOrgCoordinator(env, oa); err != nil {
			env.base.logger.Error("jobs: no runner coordinator for the organization; its experiments can not run", "org", oa.orgID, "error", err.Error())
		} else {
			l.coord, api = c, c
		}
		hostedHub(env).add(l)
		return l, api
	}
}
