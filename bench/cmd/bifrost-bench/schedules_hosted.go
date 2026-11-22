package main

// Gap lane G14: hosted experiment schedules.
//
//   - schedule.fire (hosted): the same daemon trigger as local (overlap
//     lock, the schedule's daily cap ledger, webhook alerts, records in the
//     org's .heimdall/schedules), with a runner that plans and starts the
//     experiment through the org launcher, so it runs on runners and never
//     in the server process. The org's daily spend quota caps all its
//     schedules together.
//   - the hosted ticker: one per org runtime; it stops when the runtime is
//     closed (evicted), and a platform waker rebuilds an evicted org's
//     runtime shortly before one of its schedules is due.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// hostedScheduleTimeout bounds how long a hosted scheduled experiment may
// take on the runners before it is cancelled.
var hostedScheduleTimeout = bridgeMaxTimeout

// scheduleRunnerCheck refuses a hosted fire when no runner is online (tests
// replace it).
var scheduleRunnerCheck = requireRunner

func init() {
	registerOrMergeJobKind(jobKindSpec{
		Kind: kindScheduleFire, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(1, 1)},
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			var p scheduleFireParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			f, err := webScheduleFile(env.ProjectRoot)
			if err != nil {
				return nil, "", invalid(err)
			}
			s, ok := f.Find(p.Name)
			if !ok {
				return nil, "", fmt.Errorf("%w: schedule %s", jobs.ErrNotFound, p.Name)
			}
			if err := handlers.CheckHostedSchedule(s, time.Now()); err != nil {
				return nil, "", invalid(err)
			}
			return p, "schedule:" + p.Name, nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
			var p scheduleFireParams
			if err := j.DecodeParams(&p); err != nil {
				return nil, err
			}
			rec, err := fireWebScheduleWith(ctx, env, p.Name, log)
			if err != nil {
				return nil, err
			}
			printScheduleRecord(logWriter{log}, rec)
			switch rec.Status {
			case schedule.StatusError, schedule.StatusRefused, schedule.StatusSkipped:
				return rec, fmt.Errorf("schedule %s %s: %s", rec.Schedule, rec.Status, rec.Reason)
			}
			return rec, nil
		},
	})
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		startScheduleWaker(env)
		return nil, nil
	})
}

// orgSpendCapErr refuses a hosted fire once the org's schedules together
// spent its daily quota (quota MaxSpendUSDPerDay; 0 is no cap).
func orgSpendCapErr(ctx context.Context, env jobEnv, f *schedule.File, now time.Time) error {
	if env.Services.Quota == nil {
		return nil
	}
	l, err := env.Services.Quota.Get(ctx, env.OrgID)
	if err != nil || l.MaxSpendUSDPerDay <= 0 {
		return nil
	}
	state := handlers.ScheduleStateDir(env.ProjectRoot)
	total := 0.0
	for _, s := range f.Schedules {
		v, _ := schedule.SpentToday(state, s, now)
		total += v
	}
	if total >= l.MaxSpendUSDPerDay {
		return fmt.Errorf("the organization's schedules spent %.2f USD today, at its daily cap of %.2f USD; they fire again tomorrow (ask your server owner to raise the cap on the Admin page)", total, l.MaxSpendUSDPerDay)
	}
	return nil
}

// hostedScheduleRunner is the schedule.Runner of a hosted server: every
// experiment is planned and started through the org launcher and runs on
// runners.
type hostedScheduleRunner struct {
	env jobEnv
	l   handlers.Launcher
	log jobs.Logger
}

// Run implements schedule.Runner.
func (r hostedScheduleRunner) Run(ctx context.Context, s schedule.Schedule) (*schedule.Result, error) {
	if err := handlers.CheckHostedSchedule(s, time.Now()); err != nil {
		return nil, err
	}
	if err := scheduleRunnerCheck(r.l); err != nil {
		return nil, err
	}
	a := r.env.App
	if s.Evaluation != "" {
		svc := r.env.Services
		svc.Launcher = r.l
		return runEvaluationSchedule(ctx, a, &svc, s) // X12 scheduled re-evaluations (schedule_eval.go)
	}
	if isCanarySchedule(a, s) {
		return r.runCanary(ctx, s)
	}
	exp, err := loadExperiment(schedule.Resolve(a.cfg.Root, s.Experiment))
	if err != nil {
		return nil, err
	}
	if err := s.Approved.CheckExperiment(exp); err != nil {
		return nil, err
	}
	if err := a.validatePolicy(exp); err != nil {
		return nil, err
	}
	m, err := r.l.Plan(ctx, exp)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	res := &schedule.Result{ExpID: m.ExpID}
	runErr := runExperiment(ctx, r.l, m, hostedScheduleTimeout, r.log)
	sr := scheduleRunner{a: a}
	sr.tally(ctx, res)
	if runErr != nil {
		return res, fmt.Errorf("run %s: %w", m.ExpID, runErr)
	}
	override := ""
	if s.Policy != "" {
		override = schedule.Resolve(a.cfg.Root, s.Policy)
	}
	an, _, err := a.analyzeWith(context.WithoutCancel(ctx), m.ExpID, schemas.GradeSourceOriginal, override)
	if err != nil {
		return res, fmt.Errorf("analyze %s: %w", m.ExpID, err)
	}
	res.AnalysisID, res.Decision, res.Action = an.AnalysisID, string(an.Verdict.Decision), string(an.Verdict.Action)
	if an.Policy != nil {
		res.Policy = string(an.Policy.Decision)
	}
	res.Summary = fmt.Sprintf("verdict %s, action %s, %d runs, %d run errors, cost %.4f USD", res.Decision, res.Action, res.Runs, res.RunErrors, res.CostUSD)
	return res, nil
}

// runCanary runs a scheduled canary with the hosted canary executor.
func (r hostedScheduleRunner) runCanary(ctx context.Context, s schedule.Schedule) (*schedule.Result, error) {
	a := r.env.App
	d, err := canary.Load(schedule.Resolve(a.cfg.Root, s.Experiment))
	if err != nil {
		return nil, err
	}
	opts, err := canaryScheduleOptions(d, s.Approved)
	if err != nil {
		return nil, err
	}
	cr := newCanaryRunner(a)
	cr.Exec = hostedCanaryExecutor{canaryExecutor: canaryExecutor{a: a}, l: r.l, timeout: hostedScheduleTimeout, log: r.log}
	recs, err := cr.Run(ctx, d, opts)
	res := &schedule.Result{}
	for _, rec := range recs {
		res.CostUSD += rec.CostUSD
		res.Runs += rec.Runs
		res.RunErrors += rec.RunErrors
		if rec.ExpID != "" {
			res.ExpID = rec.ExpID
		}
	}
	if len(recs) > 0 {
		last := recs[len(recs)-1]
		res.Summary = fmt.Sprintf("canary %s %s: %s", d.Name, last.State, last.Reason)
		if err == nil && (last.Outcome == canary.OutcomeError || last.Outcome == canary.OutcomeRefused) {
			err = errors.New(last.Reason)
		}
	}
	return res, err
}

// ---- ticker lifetime ----

// hostedTicker is one org runtime's running ticker.
type hostedTicker struct{ cancel context.CancelFunc }

var hostedTickers sync.Map // org id -> *hostedTicker

// startHostedScheduleTicker starts the ticker of one hosted org runtime and
// returns its stop, which the runtime's Close calls. A newer runtime of the
// same org replaces (and stops) an older one's ticker; an older runtime's
// stop never stops the newer ticker.
func startHostedScheduleTicker(a *app, s handlers.Services) func() {
	if s.Jobs == nil || a == nil || a.cfg == nil || s.OrgID == "" {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &hostedTicker{cancel: cancel}
	if old, loaded := hostedTickers.Swap(s.OrgID, h); loaded {
		old.(*hostedTicker).cancel()
	}
	t := &scheduleTicker{root: a.cfg.Root, org: s.OrgID, hosted: true, jobs: s.Jobs, logger: a.logger, last: time.Now(),
		collect: monitorCollector(a, s)} // Z22 live monitors
	go t.loop(ctx)
	return func() {
		cancel()
		hostedTickers.CompareAndDelete(s.OrgID, h)
	}
}

// hostedTickerRunning reports whether org has a live hosted ticker.
func hostedTickerRunning(org string) bool {
	_, ok := hostedTickers.Load(org)
	return ok
}

// ---- waker ----

// wakeLookahead is how far ahead the waker looks for a due schedule of an
// org with no live ticker.
func wakeLookahead() time.Duration { return 3 * tickEvery }

var scheduleWakers sync.Map // *hostedEnv -> struct{}

// startScheduleWaker rebuilds, every tick, the runtime of each hosted org
// that has no live ticker and a schedule due within wakeLookahead, so its
// ticker is running when the schedule is due. It stops with env.ctx.
func startScheduleWaker(env *hostedEnv) {
	if env == nil || env.ctx == nil || env.registry == nil || env.dir == nil || !env.layout.Hosted {
		return
	}
	if _, loaded := scheduleWakers.LoadOrStore(env, struct{}{}); loaded {
		return
	}
	go func() {
		defer scheduleWakers.Delete(env)
		tk := time.NewTicker(tickEvery)
		defer tk.Stop()
		for {
			select {
			case <-env.ctx.Done():
				return
			case now := <-tk.C:
				for _, org := range orgsToWake(env.layout, now, hostedTickerRunning) {
					wakeOrg(env, org)
				}
			}
		}
	}()
}

// orgsToWake lists the hosted orgs with no live ticker and a schedule due in
// (now, now+wakeLookahead].
func orgsToWake(l tenancy.Layout, now time.Time, running func(string) bool) []string {
	dirs, err := os.ReadDir(filepath.Join(l.StorageRoot, tenancy.ProjectsDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range dirs {
		org := d.Name()
		if !d.IsDir() || tenancy.ValidOrgID(org) != nil || running(org) {
			continue
		}
		p, err := l.Paths(org)
		if err != nil {
			continue
		}
		if dueWithin(p.ProjectRoot, now, wakeLookahead()) {
			out = append(out, org)
		}
	}
	return out
}

// dueWithin reports whether a schedule of root fires in (now, now+d].
func dueWithin(root string, now time.Time, d time.Duration) bool {
	f, err := webScheduleFile(root)
	if err != nil {
		return false
	}
	for _, s := range f.Schedules {
		c, err := schedule.ParseCron(s.Cron)
		if err != nil {
			continue
		}
		loc, err := s.Location()
		if err != nil {
			continue
		}
		if n := c.Next(now.In(loc)); !n.IsZero() && !n.After(now.Add(d)) {
			return true
		}
	}
	return false
}

// wakeOrg builds org's runtime (which starts its ticker) and releases it;
// the registry keeps it until it is idle again.
func wakeOrg(env *hostedEnv, orgID string) {
	ctx, cancel := context.WithTimeout(env.ctx, time.Minute)
	defer cancel()
	org, err := env.dir.OrgByID(ctx, orgID)
	if err != nil {
		return
	}
	_, release, err := env.registry.Acquire(ctx, org)
	if err != nil {
		env.base.logger.Warn("schedules: could not wake the organization for a due schedule", "org", orgID, "error", err.Error())
		return
	}
	release()
}
