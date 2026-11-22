package main

// Web parity lane L17: drift canary and schedule job kinds, and the
// in-server schedule ticker.
//
//   - canary.baseline (local): record or pin a canary's baseline. The hosted
//     variant (jobs_hostedops.go) records one on runners.
//   - canary.run (local): one canary look and its confirmation. The hosted
//     variant comes from lane L27; registerOrMergeJobKind joins the two.
//   - schedule.fire (local): run one web schedule (policies/schedules.yaml or
//     a scheduled canary) with the daemon's checks: no overlap, the daily
//     cap, webhook alerts.
//   - the ticker: one goroutine per org that queues the trigger job of every
//     schedule at its cron time.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	kindCanaryRun      = "canary.run"
	kindCanaryBaseline = "canary.baseline"
	kindScheduleFire   = handlers.KindScheduleFire
)

type (
	canaryRunParams struct {
		Name      string `json:"name"`
		NoConfirm bool   `json:"no_confirm,omitempty"`
	}
	canaryBaselineParams struct {
		Name string `json:"name"`
		From string `json:"from,omitempty"` // pin this existing experiment instead of running
		Note string `json:"note,omitempty"`
	}
	scheduleFireParams struct {
		Name string `json:"name"`
	}
)

func init() {
	handlers.ScheduleExperimentLoader = loadScheduled
	handlers.ScheduleExtras = addExtraSchedulesAt // canaries and live monitors (livemonitors.go)

	registerOrMergeJobKind(jobKindSpec{
		Kind: kindCanaryRun, Local: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			var p canaryRunParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			if err := checkCanary(env.ProjectRoot, p.Name); err != nil {
				return nil, "", err
			}
			return p, "canary:" + p.Name, nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
			var p canaryRunParams
			if err := j.DecodeParams(&p); err != nil {
				return nil, err
			}
			d, err := canary.Load(canary.Path(env.ProjectRoot, p.Name))
			if err != nil {
				return nil, err
			}
			log.Printf("canary %s: look starting (targets %d, trials %d)", d.Name, len(d.Targets), d.Trials)
			recs, err := newCanaryRunner(env.App).Run(ctx, d, canary.RunOptions{NoConfirm: p.NoConfirm})
			for _, r := range recs {
				printCanaryRecord(logWriter{log}, r)
			}
			if err == nil && len(recs) > 0 {
				if last := recs[len(recs)-1]; last.Outcome == canary.OutcomeError || last.Outcome == canary.OutcomeRefused {
					err = errors.New(last.Reason)
				}
			}
			return recs, err
		},
	})

	// canary.baseline: the hosted variant (jobs_hostedops.go) joins this one,
	// whatever the init order.
	registerOrMergeJobKind(jobKindSpec{
		Kind: kindCanaryBaseline, Local: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			var p canaryBaselineParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			if err := checkCanary(env.ProjectRoot, p.Name); err != nil {
				return nil, "", err
			}
			if len(p.Note) > 2000 {
				return nil, "", invalid(errors.New("note is longer than 2000 characters"))
			}
			return p, "canary:" + p.Name, nil
		},
		Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
			var p canaryBaselineParams
			if err := j.DecodeParams(&p); err != nil {
				return nil, err
			}
			d, err := canary.Load(canary.Path(env.ProjectRoot, p.Name))
			if err != nil {
				return nil, err
			}
			log.Printf("canary %s: recording the baseline", d.Name)
			rec, err := newCanaryRunner(env.App).Baseline(ctx, d, canary.BaselineOptions{From: p.From, Note: p.Note})
			if rec != nil {
				printCanaryRecord(logWriter{log}, *rec)
			}
			return rec, err
		},
	})

	registerOrMergeJobKind(jobKindSpec{
		Kind: kindScheduleFire, Local: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			var p scheduleFireParams
			if err := strictParams(raw, &p); err != nil {
				return nil, "", err
			}
			f, err := webScheduleFile(env.ProjectRoot)
			if err != nil {
				return nil, "", invalid(err)
			}
			if _, ok := f.Find(p.Name); !ok {
				return nil, "", fmt.Errorf("%w: schedule %s", jobs.ErrNotFound, p.Name)
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

	serviceHooks = append(serviceHooks, startScheduleTicker)
}

// registerOrMergeJobKind registers s, or joins it with a spec of the same
// kind already registered for the other server mode (canary.run: local
// here, hosted from lane L27): the joined spec dispatches on env.Hosted.
func registerOrMergeJobKind(s jobKindSpec) {
	jobKindsMu.Lock()
	for i, k := range jobKinds {
		if k.Kind != s.Kind {
			continue
		}
		if (k.Local && s.Local) || (k.Hosted && s.Hosted) {
			jobKindsMu.Unlock()
			panic("job kind registered twice for the same mode: " + s.Kind)
		}
		local, hosted := s, k
		if k.Local {
			local, hosted = k, s
		}
		jobKinds[i] = jobKindSpec{
			Kind: s.Kind, Local: true, Hosted: true, Opts: append(append([]jobs.KindOption(nil), local.Opts...), hosted.Opts...),
			Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
				v := local.Validate
				if env.Hosted {
					v = hosted.Validate
				}
				if v == nil {
					return raw, "", nil
				}
				return v(ctx, env, raw)
			},
			Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
				if env.Hosted {
					return hosted.Run(ctx, env, j, log, progress)
				}
				return local.Run(ctx, env, j, log, progress)
			},
		}
		jobKindsMu.Unlock()
		return
	}
	jobKindsMu.Unlock()
	registerJobKind(s)
}

// checkCanary checks a canary name names a definition of the project.
func checkCanary(root, name string) error {
	if !canary.ValidName(name) {
		return invalid(fmt.Errorf("canary name %q is not valid", name))
	}
	if _, err := canary.Load(canary.Path(root, name)); err != nil {
		return fmt.Errorf("%w: canary %s: %v", jobs.ErrNotFound, name, err)
	}
	return nil
}

// logWriter adapts a job logger to io.Writer, one Printf per write.
type logWriter struct{ log jobs.Logger }

func (w logWriter) Write(b []byte) (int, error) {
	w.log.Printf("%s", string(b))
	return len(b), nil
}

// addCanarySchedulesAt appends the scheduled canaries of root to f, as the
// CLI daemon does (canary-<name>). Broken definitions are left out.
func addCanarySchedulesAt(root string, f *schedule.File) {
	defs, _ := canary.LoadAll(filepath.Join(root, canary.DefaultDir))
	for _, d := range defs {
		if d.Schedule == nil {
			continue
		}
		if _, exists := f.Find(d.ScheduleName()); exists {
			continue
		}
		f.Schedules = append(f.Schedules, schedule.Schedule{
			Name: d.ScheduleName(), Experiment: canary.DefaultDir + "/" + d.Name + ".yaml", Cron: d.Schedule.Cron,
			Timezone: d.Schedule.Timezone, MaxUSDPerDay: d.Budget.PerDayUSD, Paused: d.Schedule.Paused,
		})
	}
}

// webScheduleFile is the org's web schedules plus its scheduled canaries
// and running live monitors.
func webScheduleFile(root string) (*schedule.File, error) {
	f, err := handlers.LoadWebSchedules(root)
	if err != nil {
		return nil, err
	}
	addExtraSchedulesAt(root, f)
	return f, nil
}

// fireWebScheduleWith runs one web schedule through a schedule daemon over
// the web file: the same overlap lock, daily cap ledger and alerts as the
// CLI daemon, with alert names resolved to org secrets. On a hosted server
// the experiment runs on runners through the org launcher
// (schedules_hosted.go) and the org's daily spend quota applies.
func fireWebScheduleWith(ctx context.Context, env jobEnv, name string, log jobs.Logger) (schedule.Record, error) {
	f, err := webScheduleFile(env.ProjectRoot)
	if err != nil {
		return schedule.Record{}, err
	}
	s, ok := f.Find(name)
	if !ok {
		return schedule.Record{}, fmt.Errorf("schedule %s: %w", name, schemas.ErrNotFound)
	}
	if err := s.Validate(env.ProjectRoot, loadScheduled); err != nil {
		return schedule.Record{}, err
	}
	if env.App == nil {
		return schedule.Record{}, errors.New("no organization app")
	}
	state := handlers.ScheduleStateDir(env.ProjectRoot)
	svc := env.Services
	var runner schedule.Runner = scheduleRunner{a: env.App, svc: &svc}
	if env.Hosted {
		if env.Services.Launcher == nil {
			return schedule.Record{}, errors.New("this organization has no launcher, so its schedules can not run on workers; ask your server owner to enable the shared worker")
		}
		if err := handlers.CheckHostedSchedule(s, time.Now()); err != nil {
			return schedule.Record{}, err
		}
		if err := orgSpendCapErr(ctx, env, f, time.Now()); err != nil {
			return schedule.Record{}, err
		}
		runner = hostedScheduleRunner{env: env, l: env.Services.Launcher, log: log}
	}
	opts := schedule.Options{
		Root: env.ProjectRoot, StateDir: state,
		Runner:   runner,
		Notifier: handlers.NewAlertSender(ctx, env.Services, state, false),
		Load:     loadScheduled,
		Logger:   env.App.logger,
	}
	if env.App.cfg != nil && !env.Hosted { // server-wide receivers are not shared across hosted orgs
		opts.ExtraAlerts = env.App.cfg.Alerts
	}
	d, err := schedule.New(&schedule.File{Alerts: f.Alerts, Schedules: []schedule.Schedule{s}}, opts)
	if err != nil {
		return schedule.Record{}, err
	}
	return d.Trigger(ctx, name)
}

// tickEvery is how often the ticker looks for due schedules.
var tickEvery = 30 * time.Second

var tickers sync.Map // org id -> struct{}

// startScheduleTicker starts the local org's ticker once, when the services
// carry the job runner (the org's router build, not a job's env). A hosted
// org's ticker belongs to its runtime (startHostedScheduleTicker, called
// from orgBuilder) so it stops when the runtime is evicted.
func startScheduleTicker(a *app, s *handlers.Services) {
	if s.Jobs == nil || a == nil || a.cfg == nil || s.Hosted {
		return
	}
	org := s.OrgID
	if org == "" {
		org = "local"
	}
	if _, loaded := tickers.LoadOrStore(org, struct{}{}); loaded {
		return
	}
	t := &scheduleTicker{root: a.cfg.Root, org: org, hosted: s.Hosted, jobs: s.Jobs, logger: a.logger, last: time.Now(),
		collect: monitorCollector(a, *s)} // Z22 live monitors
	go t.loop(context.Background())
}

// scheduleTicker queues each schedule's trigger job at its cron times. Times
// that pass while the server is down are skipped, not queued.
type scheduleTicker struct {
	root   string
	org    string
	hosted bool
	jobs   handlers.JobsAPI
	logger schemas.Logger
	last   time.Time
	warned map[string]bool
	// collect records finished live monitor checks (Z22, livemonitors.go);
	// nil collects nothing.
	collect func(context.Context, time.Time)
}

func (t *scheduleTicker) loop(ctx context.Context) {
	tk := time.NewTicker(tickEvery)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tk.C:
			t.tick(ctx, now)
			if t.collect != nil {
				t.collect(ctx, now)
			}
		}
	}
}

// tick queues every schedule with a cron time in (last, now].
func (t *scheduleTicker) tick(ctx context.Context, now time.Time) []string {
	from := t.last
	t.last = now
	f, err := webScheduleFile(t.root)
	if err != nil {
		t.warnOnce("file", "web schedules file does not load; nothing fires until it is fixed", err)
		return nil
	}
	var fired []string
	for _, s := range f.Schedules {
		if s.Paused { // listed on the page, never queued until resumed
			continue
		}
		c, err := schedule.ParseCron(s.Cron)
		if err != nil {
			continue
		}
		loc, err := s.Location()
		if err != nil {
			continue
		}
		next := c.Next(from.In(loc))
		if next.IsZero() || next.After(now) {
			continue
		}
		if t.hosted {
			if err := handlers.CheckHostedSchedule(s, now); err != nil {
				t.warnOnce(s.Name, "schedule not queued", err)
				continue
			}
		}
		kind, params := handlers.FireJobFor(s, t.hosted)
		if _, err := t.jobs.Create(ctx, jobs.NewJob{OrgID: t.org, Kind: kind, Params: params, CreatedBy: "system"}); err != nil {
			t.warnOnce(s.Name, "schedule not queued", err)
			continue
		}
		fired = append(fired, s.Name)
		if t.logger != nil {
			t.logger.Info("schedule queued", "org", t.org, "schedule", s.Name, "kind", kind)
		}
	}
	return fired
}

func (t *scheduleTicker) warnOnce(key, msg string, err error) {
	if t.warned == nil {
		t.warned = map[string]bool{}
	}
	if t.warned[key] || t.logger == nil {
		return
	}
	t.warned[key] = true
	t.logger.Warn(msg, "org", t.org, "schedule", key, "error", err.Error())
}
