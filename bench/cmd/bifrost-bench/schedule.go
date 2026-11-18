package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

func init() {
	register("schedule", "run experiments on a cron timetable with a daily spend cap and webhook alerts: schedule add|list|remove|run-now|daemon", runSchedule)
}

const scheduleUsage = `usage:
  bifrost-bench schedule add -name <name> -experiment <file> -cron "<5-field>" [-max-usd-per-day x] [-policy <file>] [-fail-on regressed,invalid] [-timezone <IANA>]
  bifrost-bench schedule list [-format table|json]
  bifrost-bench schedule remove <name>
  bifrost-bench schedule run-now <name>
  bifrost-bench schedule daemon
every subcommand also takes -config deploy/bench.yaml and -file <schedules.yaml> (default: schedules.yaml in the project root);
max_usd_per_day is required unless the experiment is a smoke experiment (docs/schedules-and-alerts.md)`

// scheduleStateDir is the daemon's state folder under the runs root: the
// spend ledger, trigger records, lock files and the alert delivery log. The
// store never lists it as an experiment (its name is not an id).
const scheduleStateDir = ".schedules"

func runSchedule(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, scheduleUsage)
		return exitUsage
	}
	switch args[0] {
	case "add":
		return scheduleAdd(args[1:], os.Stdout)
	case "list":
		return scheduleList(args[1:], os.Stdout)
	case "remove":
		return scheduleRemove(args[1:], os.Stdout)
	case "run-now":
		return scheduleRunNow(args[1:])
	case "daemon":
		return scheduleDaemon(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown schedule subcommand %q\n%s\n", args[0], scheduleUsage)
		return exitUsage
	}
}

// scheduleFlags are the flags every subcommand shares.
type scheduleFlags struct {
	fs     *flag.FlagSet
	config *string
	file   *string
}

func newScheduleFlags(name string) scheduleFlags {
	fs := flag.NewFlagSet("schedule "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return scheduleFlags{
		fs:     fs,
		config: fs.String("config", defaultConfig, "bench config"),
		file:   fs.String("file", "", "schedules file (default: schedules.yaml in the project root)"),
	}
}

// parse parses args and accepts flags after one positional name, so both
// `remove -file f nightly` and `remove nightly -file f` work.
func (f scheduleFlags) parse(args []string) error {
	if err := f.fs.Parse(args); err != nil {
		return err
	}
	if f.fs.NArg() > 1 && strings.HasPrefix(f.fs.Arg(1), "-") {
		name := f.fs.Arg(0)
		if err := f.fs.Parse(f.fs.Args()[1:]); err != nil {
			return err
		}
		return f.fs.Parse(append(append([]string{}, f.fs.Args()...), name))
	}
	return nil
}

// open loads the config and the schedules file.
func (f scheduleFlags) open() (*lib.Config, string, *schedule.File, error) {
	cfg, err := lib.LoadConfig(*f.config)
	if err != nil {
		return nil, "", nil, err
	}
	path := *f.file
	if path == "" {
		path = cfg.Path(schedule.DefaultFile)
	}
	file, err := schedule.Load(path)
	if err != nil {
		return nil, "", nil, err
	}
	return cfg, path, file, nil
}

func scheduleAdd(args []string, out io.Writer) int {
	f := newScheduleFlags("add")
	var s schedule.Schedule
	f.fs.StringVar(&s.Name, "name", "", "schedule name")
	f.fs.StringVar(&s.Experiment, "experiment", "", "experiment file")
	f.fs.StringVar(&s.Cron, "cron", "", `5-field cron expression, e.g. "0 2 * * *"`)
	f.fs.StringVar(&s.Policy, "policy", "", "release policy file overriding the experiment's")
	f.fs.StringVar(&s.Timezone, "timezone", "", "IANA timezone for the cron and the spend day (default: local)")
	f.fs.Float64Var(&s.MaxUSDPerDay, "max-usd-per-day", 0, "daily spend cap for this schedule (required unless the experiment is smoke)")
	failOn := f.fs.String("fail-on", "", "verdicts recorded as a failed check (default regressed,invalid)")
	if err := f.parse(args); err != nil || f.fs.NArg() != 0 || s.Name == "" || s.Experiment == "" || s.Cron == "" {
		fmt.Fprintln(os.Stderr, scheduleUsage)
		return exitUsage
	}
	cfg, path, file, err := f.open()
	if err != nil {
		return fail("%v", err)
	}
	s.Experiment = projectRelative(cfg.Root, s.Experiment)
	if s.Policy != "" {
		s.Policy = projectRelative(cfg.Root, s.Policy)
	}
	for _, d := range parseDecisions(*failOn) {
		s.FailOn = append(s.FailOn, string(d))
	}
	if err := file.Add(s); err != nil {
		return fail("%v", err)
	}
	if err := file.Validate(cfg.Root, loadExperiment); err != nil {
		return fail("%v", err)
	}
	if err := schedule.Save(path, file); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(out, "schedule %s added to %s; next run %s\n", s.Name, path, nextRun(s, time.Now()))
	return exitOK
}

func scheduleList(args []string, out io.Writer) int {
	f := newScheduleFlags("list")
	format := f.fs.String("format", "table", "table or json")
	if err := f.parse(args); err != nil || f.fs.NArg() != 0 || (*format != "table" && *format != "json") {
		fmt.Fprintln(os.Stderr, scheduleUsage)
		return exitUsage
	}
	cfg, path, file, err := f.open()
	if err != nil {
		return fail("%v", err)
	}
	rows, err := scheduleRows(cfg, file, time.Now())
	if err != nil {
		return fail("%v", err)
	}
	if *format == "json" {
		b, err := json.MarshalIndent(map[string]any{"file": path, "schedules": rows}, "", "  ")
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintln(out, string(b))
		return exitOK
	}
	if len(rows) == 0 {
		fmt.Fprintf(out, "no schedules in %s\n", path)
		return exitOK
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCRON\tNEXT\tEXPERIMENT\tUSD TODAY / CAP\tLAST")
	for _, r := range rows {
		capText := "none (smoke)"
		if r.MaxUSDPerDay > 0 {
			capText = fmt.Sprintf("%.2f / %.2f", r.SpentTodayUSD, r.MaxUSDPerDay)
		}
		exp := r.Experiment
		if r.Job != "" {
			exp, capText = "job "+r.Job, "none (job)" // built-in jobs spend nothing
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Cron, r.Next, exp, capText, r.Last)
	}
	if err := tw.Flush(); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

// scheduleRow is one schedule as `schedule list` shows it.
type scheduleRow struct {
	schedule.Schedule
	Next          string  `json:"next"`
	SpentTodayUSD float64 `json:"spent_today_usd"`
	Last          string  `json:"last"`
}

// scheduleRows joins each schedule with its next time, today's spend and its
// last trigger.
func scheduleRows(cfg *lib.Config, file *schedule.File, now time.Time) ([]scheduleRow, error) {
	state := filepath.Join(cfg.Path(cfg.RunsRoot), scheduleStateDir)
	last, err := schedule.LastRecords(state)
	if err != nil {
		return nil, err
	}
	rows := make([]scheduleRow, 0, len(file.Schedules))
	for _, s := range file.Schedules {
		r := scheduleRow{Schedule: s, Next: nextRun(s, now), Last: "never"}
		if r.SpentTodayUSD, err = schedule.SpentToday(state, s, now); err != nil {
			return nil, err
		}
		if rec, ok := last[s.Name]; ok {
			r.Last = rec.Started.Format(time.RFC3339) + " " + rec.Status
			if rec.Result != nil && rec.Result.Decision != "" {
				r.Last += " " + rec.Result.Decision
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// nextRun formats a schedule's next time after now, or says why it has none.
func nextRun(s schedule.Schedule, now time.Time) string {
	c, err := schedule.ParseCron(s.Cron)
	if err != nil {
		return "invalid cron"
	}
	loc, err := s.Location()
	if err != nil {
		return "invalid timezone"
	}
	next := c.Next(now.In(loc))
	if next.IsZero() {
		return "never"
	}
	return next.Format(time.RFC3339)
}

func scheduleRemove(args []string, out io.Writer) int {
	f := newScheduleFlags("remove")
	if err := f.parse(args); err != nil || f.fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, scheduleUsage)
		return exitUsage
	}
	_, path, file, err := f.open()
	if err != nil {
		return fail("%v", err)
	}
	if err := file.Remove(f.fs.Arg(0)); err != nil {
		return fail("%v", err)
	}
	if err := schedule.Save(path, file); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(out, "schedule %s removed from %s\n", f.fs.Arg(0), path)
	return exitOK
}

func scheduleRunNow(args []string) int {
	f := newScheduleFlags("run-now")
	debug := f.fs.Bool("debug", false, "debug logging")
	if err := f.parse(args); err != nil || f.fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, scheduleUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, d, err := openScheduleDaemon(ctx, *f.config, *f.file, *debug)
	if err != nil {
		return fail("%v", err)
	}
	rec, err := d.Trigger(ctx, f.fs.Arg(0))
	if err != nil {
		return fail("%v", err)
	}
	printScheduleRecord(os.Stdout, rec)
	switch rec.Status {
	case schedule.StatusOK:
		return exitOK
	case schedule.StatusFailed:
		return 1
	default:
		return exitInfra
	}
}

func scheduleDaemon(args []string) int {
	f := newScheduleFlags("daemon")
	debug := f.fs.Bool("debug", false, "debug logging")
	if err := f.parse(args); err != nil || f.fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, scheduleUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, d, err := openScheduleDaemon(ctx, *f.config, *f.file, *debug)
	if err != nil {
		return fail("%v", err)
	}
	a.logger.Info("schedule daemon started; stop with Ctrl-C")
	if err := d.Run(ctx); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

// startScheduleDaemon loads the schedules file at path (empty: schedules.yaml
// in the project root), validates it and runs its daemon in the background
// until ctx ends, for `serve` with the daemon flag. It returns at once; a
// file that does not load or validate is an error and nothing starts.
func startScheduleDaemon(ctx context.Context, a *app, path string) error {
	d, err := newScheduleDaemon(a, path)
	if err != nil {
		return err
	}
	go func() {
		if err := d.Run(ctx); err != nil {
			a.logger.Error("schedule daemon stopped", "error", err.Error())
		}
	}()
	a.logger.Info("schedule daemon started inside serve")
	return nil
}

// openScheduleDaemon builds the app with the live gateway and the daemon.
func openScheduleDaemon(ctx context.Context, config, path string, debug bool) (*app, *schedule.Daemon, error) {
	a, err := newApp(ctx, config, appOptions{gateway: true, debug: debug})
	if err != nil {
		return nil, nil, err
	}
	d, err := newScheduleDaemon(a, path)
	if err != nil {
		return nil, nil, err
	}
	return a, d, nil
}

// newScheduleDaemon loads and validates the schedules file and wires the
// daemon: the check runner, the webhook sender (its log beside the spend
// ledger) and bench.yaml's alerts as receivers for every schedule.
func newScheduleDaemon(a *app, path string) (*schedule.Daemon, error) {
	if path == "" {
		path = a.cfg.Path(schedule.DefaultFile)
	}
	file, err := schedule.Load(path)
	if err != nil {
		return nil, err
	}
	addInboxSchedule(a, file) // Failure inbox area: inbox/sources.yaml schedule: becomes an inbox-pull job (inbox.go)
	// W5 drift canary: canaries/*.yaml with a schedule run as canary-<name>;
	// loadScheduled reads canary files too (canary.go).
	addCanarySchedules(a, file)
	addLivescoreSchedule(a, file) // X6 live scoring: livescore/config.yaml schedule: becomes a livescore-pull job (livescore.go)
	if err := file.Validate(a.cfg.Root, loadScheduled); err != nil {
		return nil, fmt.Errorf("schedules %s: %w", path, err)
	}
	for _, c := range a.cfg.Alerts {
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("bench config alerts: %w", err)
		}
	}
	state := filepath.Join(a.store.Root(), scheduleStateDir)
	return schedule.New(file, schedule.Options{
		Root:        a.cfg.Root,
		StateDir:    state,
		Runner:      scheduleRunner{a: a},
		Notifier:    alerts.NewSender(filepath.Join(state, schedule.DeliveriesFile)),
		Load:        loadScheduled, // W5 drift canary: a canary file's caps (canary.go)
		Logger:      a.logger,
		ExtraAlerts: a.cfg.Alerts,
	})
}

// printScheduleRecord prints one trigger's outcome.
func printScheduleRecord(w io.Writer, rec schedule.Record) {
	fmt.Fprintf(w, "schedule %s: %s", rec.Schedule, rec.Status)
	if r := rec.Result; r != nil {
		if r.ExpID != "" {
			fmt.Fprintf(w, ", experiment %s", r.ExpID)
		}
		if r.Decision != "" {
			fmt.Fprintf(w, ", verdict %s (%s)", r.Decision, r.Action)
		}
		if r.Policy != "" {
			fmt.Fprintf(w, ", policy %s", r.Policy)
		}
		fmt.Fprintf(w, ", %d runs, %d run errors, cost %.4f USD", r.Runs, r.RunErrors, r.CostUSD)
	}
	fmt.Fprintf(w, ", spent today %.4f USD", rec.SpentTodayUSD)
	if rec.Reason != "" {
		fmt.Fprintf(w, "\n  reason: %s", rec.Reason)
	}
	if len(rec.Alerts) > 0 {
		fmt.Fprintf(w, "\n  alerts: %s", strings.Join(rec.Alerts, ", "))
	}
	fmt.Fprintln(w)
}

// scheduleRunner runs a schedule the way `check` runs an experiment file:
// plan and freeze a new experiment, run every slot under its spend cap, then
// analyse it with the schedule's policy override, and update the results
// index.
type scheduleRunner struct {
	a *app
}

// Run implements schedule.Runner. After the freeze a failure still returns
// the Result with the spend so far, so the daily cap counts it.
func (r scheduleRunner) Run(ctx context.Context, s schedule.Schedule) (*schedule.Result, error) {
	if s.Job == schedule.JobLivescorePull {
		return runLivescoreSchedule(ctx, r.a, s) // X6 live scoring (livescore.go)
	}
	if s.Job != "" {
		return runScheduleJob(ctx, r.a, s) // built-in jobs, e.g. the failure inbox's inbox-pull (inbox.go)
	}
	if isCanarySchedule(r.a, s) {
		return runCanarySchedule(ctx, r.a, s) // W5 drift canary: a canary look and its confirmation (canary.go)
	}
	a := r.a
	exp, err := loadExperiment(schedule.Resolve(a.cfg.Root, s.Experiment))
	if err != nil {
		return nil, err
	}
	if err := a.validatePolicy(exp); err != nil {
		return nil, err
	}
	m, err := a.newPlanner().Plan(ctx, exp)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	if err := a.store.CreateExperiment(ctx, m); err != nil {
		return nil, fmt.Errorf("freeze %s: %w", m.ExpID, err)
	}
	res := &schedule.Result{ExpID: m.ExpID}
	sum, runErr := r.execute(ctx, m)
	if sum != nil {
		res.CostUSD, res.RunErrors = sum.SpentUSD, sum.Errors+sum.LeaseLost
	}
	defer refreshResultsIndex(context.WithoutCancel(ctx), a, m.ExpID)
	if runErr != nil {
		r.tally(ctx, res)
		return res, fmt.Errorf("run %s: %w", m.ExpID, runErr)
	}
	override := ""
	if s.Policy != "" {
		override = schedule.Resolve(a.cfg.Root, s.Policy)
	}
	an, _, err := a.analyzeWith(context.WithoutCancel(ctx), m.ExpID, schemas.GradeSourceOriginal, override)
	r.tally(ctx, res)
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

// execute runs every slot of the frozen experiment and returns the engine's
// summary, whose SpentUSD counts unknown costs at the per-run estimate.
func (r scheduleRunner) execute(ctx context.Context, m *schemas.Manifest) (*engine.Summary, error) {
	a := r.a
	events := make(chan engine.Event, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			if ev.Kind != "" {
				a.logger.Debug(progressLine(ev), "experiment", m.ExpID)
			}
		}
	}()
	janitor := engine.NewJanitor(a.reg.Gateway, a.docker, a.logger)
	sched := engine.NewScheduler(a.reg, a.cfg.Root, a.logger, janitor)
	sum, err := sched.Run(ctx, m, engine.RunOptions{Resume: true, Events: events})
	close(events)
	<-done
	return sum, err
}

// tally adds the sealed runs' count, infrastructure errors and observed cost
// to res. The cost kept is the larger of the engine's spend and the observed
// sum, so the daily cap never undercounts.
func (r scheduleRunner) tally(ctx context.Context, res *schedule.Result) {
	runs, err := r.a.store.SealedRuns(context.WithoutCancel(ctx), res.ExpID, schemas.GradeSourceOriginal)
	if err != nil {
		r.a.logger.Warn("scheduled run tally incomplete", "experiment", res.ExpID, "error", err.Error())
		return
	}
	observed := 0.0
	res.Runs = len(runs)
	for _, sr := range runs {
		if sr.Run.Outcome == schemas.OutcomeInfraError || sr.Run.Outcome == schemas.OutcomeAbortedInfra {
			res.RunErrors++
		}
		if v, ok := sr.Metrics["cost_usd"].Float(); ok {
			observed += v
		}
	}
	res.CostUSD = max(res.CostUSD, observed)
}

// projectRelative turns a path given on the command line into one relative
// to the project root when it lies inside it, so schedules.yaml stays valid
// wherever the command runs from.
func projectRelative(root, p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs
	}
	return filepath.ToSlash(rel)
}
