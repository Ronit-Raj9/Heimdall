package main

// W5 drift canary (bench/canary, docs/drift-canary.md): the `canary`
// command, the engine-backed canary.Executor, the schedule daemon hook and
// the serve source for /api/bench/canaries.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

func init() {
	register("canary", "drift canary: re-run a pinned suite on a schedule and test it against a known-good baseline: canary init|list|status|baseline|run|simulate", runCanary)
}

const canaryUsage = `usage:
  bifrost-bench canary init <name> -agent <file> -suite <id@version> -tasks t01,t02,...|all [-tier <tier>] [-trials n]
        -target <provider>/<model> [-target ...] [-cron "<5-field>"] [-timezone <IANA>]
        -per-run-usd x -per-day-usd y [-smoke] [-sensitivity low|normal|high] [-force]
  bifrost-bench canary list [-format table|json]
  bifrost-bench canary status [<name>] [-format table|json]
  bifrost-bench canary baseline <name> [-from <exp_id>] [-note text]
  bifrost-bench canary run <name> [-no-confirm] [-inject-fault <probe>]
  bifrost-bench canary simulate [-tasks 8] [-trials 1] [-triggers 365] [-reps 400] [-sensitivity normal] [-drop 0] [-latency-shift 0] [-spread]
every subcommand also takes -config deploy/bench.yaml; definitions live in canaries/<name>.yaml (docs/drift-canary.md).
run exits 0 steady (or baseline_stale), 1 drifted, 2 watching, 4 invalid, refused or error.`

// canaryStateDir is the canaries' state folder under the runs root. The
// store never lists it as an experiment (its name is not an id).
const canaryStateDir = ".canaries"

func runCanary(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, canaryUsage)
		return exitUsage
	}
	switch args[0] {
	case "init":
		return canaryInit(args[1:], os.Stdout)
	case "list":
		return canaryStatus(args[1:], os.Stdout, true)
	case "status":
		return canaryStatus(args[1:], os.Stdout, false)
	case "baseline":
		return canaryBaseline(args[1:], os.Stdout)
	case "run":
		return canaryRunCmd(args[1:], os.Stdout)
	case "simulate":
		return canarySimulate(args[1:], os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown canary subcommand %q\n%s\n", args[0], canaryUsage)
		return exitUsage
	}
}

// canaryFlags parse flags before or after one positional name.
type canaryFlags struct {
	fs     *flag.FlagSet
	config *string
}

func newCanaryFlags(name string) canaryFlags {
	fs := flag.NewFlagSet("canary "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return canaryFlags{fs: fs, config: fs.String("config", defaultConfig, "bench config")}
}

// parse accepts `<name> -flag v` and `-flag v <name>`.
func (f canaryFlags) parse(args []string) (string, error) {
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := f.fs.Parse(args); err != nil {
		return "", err
	}
	if name == "" && f.fs.NArg() > 0 {
		name = f.fs.Arg(0)
		if err := f.fs.Parse(f.fs.Args()[1:]); err != nil {
			return "", err
		}
	}
	if f.fs.NArg() > 0 {
		return "", fmt.Errorf("unexpected arguments %v", f.fs.Args())
	}
	return name, nil
}

// repeated collects a repeatable flag.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

func canaryInit(args []string, out io.Writer) int {
	f := newCanaryFlags("init")
	d := &canary.Definition{}
	f.fs.StringVar(&d.Agent, "agent", "", "agent config file")
	f.fs.StringVar(&d.Suite, "suite", "", "suite id@version")
	tasks := f.fs.String("tasks", "", "comma-separated task ids, or all")
	f.fs.StringVar(&d.Tier, "tier", "", "suite tier whose tasks `all` means")
	f.fs.IntVar(&d.Trials, "trials", 1, "trials per task")
	var targets repeated
	f.fs.Var(&targets, "target", "provider/model to watch; repeat for several (same model via two providers = equivalence check)")
	cron := f.fs.String("cron", "", `5-field cron expression, e.g. "0 6 * * *"`)
	tz := f.fs.String("timezone", "", "IANA timezone of the cron and the spend day")
	f.fs.Float64Var(&d.Budget.PerRunUSD, "per-run-usd", 0, "spend cap of one run (required unless -smoke)")
	f.fs.Float64Var(&d.Budget.PerDayUSD, "per-day-usd", 0, "spend cap of one day, confirmations included (required unless -smoke)")
	f.fs.BoolVar(&d.Smoke, "smoke", false, "scripted, zero-cost canary; never a finding")
	f.fs.StringVar(&d.Sensitivity, "sensitivity", "", "low, normal (default) or high")
	f.fs.StringVar(&d.Description, "description", "", "one line about what the canary watches")
	force := f.fs.Bool("force", false, "overwrite an existing definition")
	name, err := f.parse(args)
	if err != nil || name == "" || *tasks == "" || len(targets) == 0 {
		fmt.Fprintln(os.Stderr, canaryUsage)
		return exitUsage
	}
	d.Name = name
	if *tasks == "all" {
		d.Tasks = schemas.TaskSelector{All: true}
	} else {
		for _, t := range strings.Split(*tasks, ",") {
			if t = strings.TrimSpace(t); t != "" {
				d.Tasks.IDs = append(d.Tasks.IDs, t)
			}
		}
	}
	for _, t := range targets {
		prov, model, ok := strings.Cut(t, "/")
		if !ok {
			prov, model = "", t
		}
		d.Targets = append(d.Targets, canary.Target{Provider: prov, Model: model})
	}
	if *cron != "" {
		d.Schedule = &canary.ScheduleSpec{Cron: *cron, Timezone: *tz}
	}
	cfg, err := loadConfig(*f.config)
	if err != nil {
		return fail("%v", err)
	}
	d.Agent = projectRelative(cfg.Root, d.Agent)
	if err := d.Validate(); err != nil {
		return fail("%v", err)
	}
	path := canary.Path(cfg.Root, name)
	if _, err := os.Stat(path); err == nil && !*force {
		return fail("canary %s already exists at %s; pass -force to overwrite", name, path)
	}
	if err := canary.Save(path, d); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(out, "canary %s written to %s\nnext: bifrost-bench canary baseline %s\n", name, path, name)
	if d.Schedule != nil {
		fmt.Fprintf(out, "the schedule daemon (serve -schedules, or schedule daemon) runs it on %q as schedule %s\n", d.Schedule.Cron, d.ScheduleName())
	}
	return exitOK
}

func canaryStatus(args []string, out io.Writer, listOnly bool) int {
	f := newCanaryFlags("status")
	format := f.fs.String("format", "table", "table or json")
	name, err := f.parse(args)
	if err != nil || (*format != "table" && *format != "json") || (listOnly && name != "") {
		fmt.Fprintln(os.Stderr, canaryUsage)
		return exitUsage
	}
	cfg, err := loadConfig(*f.config)
	if err != nil {
		return fail("%v", err)
	}
	src := canarySource{root: cfg.Root, state: canaryStateRoot(cfg)}
	ctx := context.Background()
	if name != "" {
		det, err := src.Get(ctx, name)
		if err != nil {
			return fail("%v", err)
		}
		if *format == "json" {
			return printJSON(det, "canary status")
		}
		printCanaryDetail(out, det)
		return exitOK
	}
	l, err := src.List(ctx)
	if err != nil {
		return fail("%v", err)
	}
	if *format == "json" {
		return printJSON(l, "canary list")
	}
	if len(l.Canaries) == 0 && len(l.Errors) == 0 {
		fmt.Fprintf(out, "no canaries in %s\n", filepath.Join(cfg.Root, canary.DefaultDir))
		return exitOK
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tTARGETS\tBASELINE\tLOOKS\tNEXT\tUSD TODAY / CAP\tREASON")
	for _, s := range l.Canaries {
		var ts []string
		for _, t := range s.Targets {
			ts = append(ts, t.Label())
		}
		base := "none"
		if s.Baseline != nil {
			base = s.Baseline.ExpID
		}
		capText := fmt.Sprintf("%.2f / %.2f", s.SpentTodayUSD, s.PerDayUSD)
		if s.PerDayUSD == 0 {
			capText = "none (smoke)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n", s.Name, s.State, strings.Join(ts, ", "), base, s.Looks, canaryOr(s.Next, "not scheduled"), capText, truncate(s.Reason, 90))
	}
	for _, e := range l.Errors {
		fmt.Fprintf(tw, "%s\tdefinition error\t\t\t\t\t\t%s\n", e.Name, truncate(e.Error, 90))
	}
	if err := tw.Flush(); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

func canaryBaseline(args []string, out io.Writer) int {
	f := newCanaryFlags("baseline")
	from := f.fs.String("from", "", "pin this existing experiment (same frozen config) instead of running")
	note := f.fs.String("note", "", "why this baseline is known-good")
	debug := f.fs.Bool("debug", false, "debug logging")
	name, err := f.parse(args)
	if err != nil || name == "" {
		fmt.Fprintln(os.Stderr, canaryUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *f.config, appOptions{gateway: true, debug: *debug})
	if err != nil {
		return fail("%v", err)
	}
	d, err := canary.Load(canary.Path(a.cfg.Root, name))
	if err != nil {
		return fail("%v", err)
	}
	rec, err := newCanaryRunner(a).Baseline(ctx, d, canary.BaselineOptions{From: *from, Note: *note})
	if rec != nil {
		printCanaryRecord(out, *rec)
	}
	if err != nil {
		return fail("%v", err)
	}
	return exitOK
}

func canaryRunCmd(args []string, out io.Writer) int {
	f := newCanaryFlags("run")
	noConfirm := f.fs.Bool("no-confirm", false, "do not take the immediate confirmation look after a flag")
	inject := f.fs.String("inject-fault", "", "smoke canaries only: replace the watched condition with this probe to force a drift (tests the alarm path)")
	debug := f.fs.Bool("debug", false, "debug logging")
	name, err := f.parse(args)
	if err != nil || name == "" {
		fmt.Fprintln(os.Stderr, canaryUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *f.config, appOptions{gateway: true, debug: *debug})
	if err != nil {
		return fail("%v", err)
	}
	d, err := canary.Load(canary.Path(a.cfg.Root, name))
	if err != nil {
		return fail("%v", err)
	}
	recs, err := newCanaryRunner(a).Run(ctx, d, canary.RunOptions{Inject: *inject, NoConfirm: *noConfirm})
	for _, r := range recs {
		printCanaryRecord(out, r)
	}
	if err != nil {
		return fail("%v", err)
	}
	return canaryExit(recs[len(recs)-1])
}

// canaryExit maps the last record to the exit code.
func canaryExit(r canary.Record) int {
	switch {
	case r.Outcome == canary.OutcomeRefused || r.Outcome == canary.OutcomeError || r.State == canary.StateInvalid:
		return exitInfra
	case r.State == canary.StateDrifted:
		return 1
	case r.State == canary.StateWatching:
		return 2
	}
	return exitOK
}

func canarySimulate(args []string, out io.Writer) int {
	f := newCanaryFlags("simulate")
	c := canary.SimConfig{}
	f.fs.IntVar(&c.Tasks, "tasks", 8, "tasks")
	f.fs.IntVar(&c.Trials, "trials", 1, "trials per task")
	f.fs.IntVar(&c.Triggers, "triggers", 365, "scheduled triggers per replication")
	f.fs.IntVar(&c.Reps, "reps", 400, "replications")
	f.fs.StringVar(&c.Sensitivity, "sensitivity", canary.SensitivityNormal, "low, normal or high")
	f.fs.Float64Var(&c.DropPoints, "drop", 0, "true success drop on every task, 0..1 (0: no drift, the false-alarm study)")
	f.fs.Float64Var(&c.ShiftRel, "latency-shift", 0, "true relative latency rise (0: none)")
	f.fs.BoolVar(&c.Spread, "spread", false, "true task success spread over 0.15..0.85 instead of 0.75..0.95")
	format := f.fs.String("format", "table", "table or json")
	if _, err := f.parse(args); err != nil {
		fmt.Fprintln(os.Stderr, canaryUsage)
		return exitUsage
	}
	if err := checkSimulate(c, *format); err != nil {
		fmt.Fprintln(os.Stderr, "canary simulate:", err)
		return exitUsage
	}
	c.Seed = 1
	r := canary.Simulate(c)
	if *format == "json" {
		return printJSON(r, "canary simulate")
	}
	// Print the configuration the simulator ran, not the raw flags.
	c = r.Config
	fmt.Fprintf(out, "%d tasks x %d trials, sensitivity %s (family level %.2f), %d replications x %d triggers\n", c.Tasks, c.Trials, c.Sensitivity, r.Alpha, c.Reps, c.Triggers)
	fmt.Fprintf(out, "single-look flag rate      %.4f\nconfirmation looks/trigger %.4f\nalarms per trigger         %.5f\n", r.FlagPerLook, r.ConfirmPerTrigger, r.AlarmPerTrigger)
	if c.DropPoints > 0 || c.ShiftRel > 0 {
		fmt.Fprintf(out, "alarmed on first trigger   %.3f\nalarmed within 3 triggers  %.3f\n", r.DetectFirst, r.DetectBy3)
	} else {
		fmt.Fprintf(out, "P(any false alarm, 30 daily triggers)  %.3f%s\nP(any false alarm, 365 daily triggers) %.3f%s\n",
			r.AnyAlarm30, horizonNote(r.Measured30), r.AnyAlarm365, horizonNote(r.Measured365))
	}
	return exitOK
}

// horizonNote labels a horizon chance extrapolated from the per-trigger
// rate because the replications were shorter than the horizon.
func horizonNote(measured bool) string {
	if measured {
		return ""
	}
	return " (extrapolated, assumes independent triggers; raise -triggers to measure)"
}

// checkSimulate rejects dimensions the simulator would silently replace and
// formats it cannot print.
func checkSimulate(c canary.SimConfig, format string) error {
	for _, d := range []struct {
		name string
		v    int
	}{{"tasks", c.Tasks}, {"trials", c.Trials}, {"triggers", c.Triggers}, {"reps", c.Reps}} {
		if d.v <= 0 {
			return fmt.Errorf("-%s must be positive, got %d", d.name, d.v)
		}
	}
	if c.Sensitivity != "" && !slices.Contains(canary.Sensitivities, c.Sensitivity) {
		return fmt.Errorf("-sensitivity %q is not one of %s", c.Sensitivity, strings.Join(canary.Sensitivities, ", "))
	}
	if format != "table" && format != "json" {
		return fmt.Errorf("-format %q is not table or json", format)
	}
	return nil
}

// printCanaryRecord prints one look.
func printCanaryRecord(w io.Writer, r canary.Record) {
	fmt.Fprintf(w, "canary %s %s: %s -> state %s", r.Canary, r.Kind, r.Outcome, r.State)
	if r.ExpID != "" {
		fmt.Fprintf(w, ", experiment %s", r.ExpID)
	}
	if r.Baseline != "" && r.Kind != canary.KindBaseline {
		fmt.Fprintf(w, " vs baseline %s", r.Baseline)
	}
	fmt.Fprintf(w, ", %d runs, %d run errors, cost %.4f USD", r.Runs, r.RunErrors, r.CostUSD)
	if r.Injected != "" {
		fmt.Fprintf(w, ", injected %s (forced drift, synthetic)", r.Injected)
	}
	if r.Reason != "" {
		fmt.Fprintf(w, "\n  %s", r.Reason)
	}
	if r.Comparison != nil {
		for _, t := range r.Comparison.Tests {
			p := "-"
			if t.P != nil {
				p = fmt.Sprintf("%.4f", *t.P)
			}
			fmt.Fprintf(w, "\n  %-12s %-11s %s/%s p %s: %s", t.Status, t.Metric, t.Label, t.Condition, p, t.Reason)
		}
	}
	if r.Changes != nil && len(r.Changes.Moved) > 0 {
		fmt.Fprintf(w, "\n  what changed:")
		for _, m := range r.Changes.Moved {
			fmt.Fprintf(w, "\n    - %s", m)
		}
	}
	for _, e := range r.Equivalence {
		fmt.Fprintf(w, "\n  equivalence %s: %s", e.Verdict, e.Reason)
	}
	if len(r.Alerts) > 0 {
		fmt.Fprintf(w, "\n  alerts: %s", strings.Join(r.Alerts, ", "))
	}
	fmt.Fprintln(w)
}

// printCanaryDetail prints `canary status <name>`.
func printCanaryDetail(w io.Writer, det *canary.Detail) {
	s := det.Summary
	fmt.Fprintf(w, "canary %s: %s\n  %s\n", s.Name, s.State, s.Reason)
	fmt.Fprintf(w, "  suite %s, agent %s, %d tasks x %d trials, sensitivity %s (level %.2f)\n", s.Suite, s.Agent, s.Tasks, s.Trials, s.Sensitivity, s.Alpha)
	if s.Baseline != nil {
		fmt.Fprintf(w, "  baseline %s pinned %s (%d looks since)\n", s.Baseline.ExpID, s.Baseline.PinnedAt.Format(time.RFC3339), s.Looks)
	}
	capText := fmt.Sprintf("of %.2f", s.PerDayUSD)
	if s.PerDayUSD == 0 {
		capText = "(no cap: smoke, priced at the snapshot, nothing billed)"
	}
	fmt.Fprintf(w, "  spent today %.4f USD %s; next %s\n", s.SpentTodayUSD, capText, canaryOr(s.Next, "not scheduled"))
	for i, r := range det.Records {
		if i >= 10 {
			break
		}
		fmt.Fprintf(w, "  %s %-12s %-8s %-14s %s\n", r.Started.Format(time.RFC3339), r.Kind, r.Outcome, r.State, canaryOr(r.ExpID, "-"))
	}
}

func canaryOr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

// canaryStateRoot is the state folder under the runs root.
func canaryStateRoot(cfg *lib.Config) string {
	return filepath.Join(cfg.Path(cfg.RunsRoot), canaryStateDir)
}

// newCanaryRunner wires the runner: the engine executor, the webhook sender
// (its log beside the canaries' state) and bench.yaml's alerts.
func newCanaryRunner(a *app) *canary.Runner {
	state := filepath.Join(a.store.Root(), canaryStateDir)
	return &canary.Runner{
		Root: state, Exec: canaryExecutor{a: a}, Logger: a.logger, ExtraAlerts: a.cfg.Alerts,
		Notifier: alerts.NewSender(filepath.Join(state, "deliveries.jsonl")),
	}
}

// canaryExecutor implements canary.Executor with the planner, the
// scheduler and the run store.
type canaryExecutor struct {
	a *app
}

// Plan implements canary.Executor.
func (x canaryExecutor) Plan(ctx context.Context, d *canary.Definition, inject string) (*canary.Plan, error) {
	a := x.a
	var tasks []string
	if d.Tier != "" {
		s, err := engine.LoadSuite(ctx, a.reg, d.Suite)
		if err != nil {
			return nil, err
		}
		sel, ok := s.Tiers[d.Tier]
		if !ok {
			return nil, fmt.Errorf("suite %s has no tier %q", d.Suite, d.Tier)
		}
		tasks = sel.Resolve(s.TaskIDs)
	}
	m, err := a.newPlanner().PlanCanary(ctx, d.Experiment(tasks, inject))
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	p := &canary.Plan{ExpID: m.ExpID, ManifestHash: m.Hash, Fingerprint: canary.Fingerprint(m, d.Rename(inject)),
		Slots: canary.CountedSlots(m), Rename: d.Rename(inject), Manifest: m}
	if f, err := a.forecast(ctx, m); err == nil && f != nil && f.CostUSD.High != nil {
		p.ForecastUSD, p.ForecastNote = f.CostUSD.High, f.Basis
	}
	return p, nil
}

// Execute implements canary.Executor: freeze, run every slot under the
// per-run cap, and count the spend as the schedule daemon does (the larger
// of the engine's spend and the observed sum).
func (x canaryExecutor) Execute(ctx context.Context, p *canary.Plan) (string, float64, error) {
	a := x.a
	m, ok := p.Manifest.(*schemas.Manifest)
	if !ok || m == nil {
		return "", 0, errors.New("canary plan carries no manifest")
	}
	if err := a.store.CreateExperiment(ctx, m); err != nil {
		return "", 0, fmt.Errorf("freeze %s: %w", m.ExpID, err)
	}
	defer refreshResultsIndex(context.WithoutCancel(ctx), a, m.ExpID)
	res := &schedule.Result{ExpID: m.ExpID}
	sum, runErr := scheduleRunner{a: a}.execute(ctx, m)
	if sum != nil {
		res.CostUSD = sum.SpentUSD
	}
	scheduleRunner{a: a}.tally(ctx, res)
	if runErr != nil {
		return m.ExpID, res.CostUSD, fmt.Errorf("run %s: %w", m.ExpID, runErr)
	}
	return m.ExpID, res.CostUSD, nil
}

// Observe implements canary.Executor: the counted sealed runs, each checked
// against its seal, with their gateway rows and failure clusters.
func (x canaryExecutor) Observe(ctx context.Context, expID string, rename map[string]string) (*canary.Execution, error) {
	return observeCanary(ctx, x.a.store, expID, rename)
}

// observeCanary reads one experiment for the canary from the run store.
func observeCanary(ctx context.Context, st *store.FSStore, expID string, rename map[string]string) (*canary.Execution, error) {
	m, err := st.LoadManifest(ctx, expID)
	if err != nil {
		return nil, err
	}
	runs, err := st.SealedRuns(ctx, expID, store.GradeSourceOriginal)
	if err != nil {
		return nil, fmt.Errorf("load sealed runs: %w", err)
	}
	ex := &canary.Execution{ExpID: m.ExpID, ManifestHash: m.Hash, Fingerprint: canary.Fingerprint(m, rename), CreatedAt: m.CreatedAt,
		Smoke: m.Experiment.Smoke, Slots: canary.CountedSlots(m), Arms: map[string]string{}, Runs: []canary.RunObs{}, Clusters: []canary.ClusterRef{}}
	for id, arm := range m.Arms {
		ex.Arms[id] = arm.Label
	}
	if err := st.VerifyChain(expID); err != nil {
		ex.Problems = append(ex.Problems, "seals.log chain does not verify: "+err.Error())
	}
	dirs := map[string]string{}
	for _, sr := range runs {
		if !sr.Slot.Counted {
			continue
		}
		dirs[sr.Run.RunID] = sr.Dir
		ex.Runs = append(ex.Runs, canary.Observe(sr, readRequestLog(sr.Dir), rename))
	}
	src := canaryClusterSource{m: m, runs: runs, dirs: dirs}
	rep, err := clusters.Build(ctx, src, clusters.Query{Experiments: []string{expID}, Synthetic: clusters.SyntheticInclude, Min: 1, Now: time.Now()})
	if err == nil {
		for _, c := range rep.Clusters {
			ex.Clusters = append(ex.Clusters, canary.ClusterRef{Signature: c.Signature, Title: c.Title, Count: c.Count, Tasks: c.Tasks})
		}
	}
	return ex, nil
}

// readRequestLog reads the gateway rows from a run's evidence.json; nil when
// the run has none.
func readRequestLog(dir string) *schemas.RequestLog {
	b, err := os.ReadFile(filepath.Join(dir, "evidence.json"))
	if err != nil {
		return nil
	}
	var ev struct {
		Requests *schemas.RequestLog `json:"requests"`
	}
	if json.Unmarshal(b, &ev) != nil {
		return nil
	}
	return ev.Requests
}

// canaryClusterSource is a clusters.Source over one experiment's sealed
// runs, so a look does not rebuild the whole run index.
type canaryClusterSource struct {
	m    *schemas.Manifest
	runs []schemas.SealedRun
	dirs map[string]string
}

func (s canaryClusterSource) Experiments() []string { return []string{s.m.ExpID} }

func (s canaryClusterSource) Manifest(expID string) (*schemas.Manifest, bool) {
	return s.m, expID == s.m.ExpID
}

func (s canaryClusterSource) VerifiedRuns(_ context.Context, expID string) ([]schemas.SealedRun, error) {
	if expID != s.m.ExpID {
		return nil, schemas.ErrNotFound
	}
	return s.runs, nil
}

func (s canaryClusterSource) RunEvidence(runID, name string) ([]byte, error) {
	dir, ok := s.dirs[runID]
	if !ok || !clusterEvidenceFiles[name] {
		return nil, fmt.Errorf("run %s file %s: %w", runID, name, schemas.ErrNotFound)
	}
	p := filepath.Join(dir, name)
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("run %s file %s: not a regular file: %w", runID, name, schemas.ErrNotFound)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, clusterEvidenceMax))
}

// Schedule daemon hook: every canary with a schedule becomes a daemon entry
// named canary-<name> whose experiment is its definition file; the daily cap
// is its per_day_usd. loadScheduled reads such a file as the experiment the
// daemon's cap check needs, and the runner hands it to the canary runner.

// addCanarySchedules appends the scheduled canaries to the daemon's file.
// A definition that does not load is logged and left out.
func addCanarySchedules(a *app, file *schedule.File) {
	defs, errs := canary.LoadAll(filepath.Join(a.cfg.Root, canary.DefaultDir))
	for name, e := range errs {
		a.logger.Warn("canary definition not scheduled", "canary", name, "error", e)
	}
	for _, d := range defs {
		if d.Schedule == nil {
			continue
		}
		if _, exists := file.Find(d.ScheduleName()); exists {
			continue
		}
		file.Schedules = append(file.Schedules, schedule.Schedule{
			Name: d.ScheduleName(), Experiment: canary.DefaultDir + "/" + d.Name + ".yaml", Cron: d.Schedule.Cron,
			Timezone: d.Schedule.Timezone, MaxUSDPerDay: d.Budget.PerDayUSD,
		})
	}
}

// isCanaryFile reports whether a schedule's experiment path names a canary
// definition (canaries/<name>.yaml).
func isCanaryFile(path string) bool {
	return filepath.Base(filepath.Dir(path)) == canary.DefaultDir && filepath.Ext(path) == ".yaml"
}

// loadScheduled is the daemon's experiment loader: a canary definition
// reads as an experiment with its smoke flag and its per-run cap, so the
// daemon's checks (max_usd_per_day required, cap fits the day) hold for it.
func loadScheduled(path string) (*schemas.Experiment, error) {
	if !isCanaryFile(path) {
		return loadExperiment(path)
	}
	d, err := canary.Load(path)
	if err != nil {
		return nil, err
	}
	return &schemas.Experiment{Name: "canary-" + d.Name, Smoke: d.Smoke, Budget: schemas.BudgetSpec{TotalUSD: d.Budget.PerRunUSD}}, nil
}

// isCanarySchedule reports whether a daemon schedule is a canary.
func isCanarySchedule(a *app, s schedule.Schedule) bool {
	return s.Job == "" && isCanaryFile(schedule.Resolve(a.cfg.Root, s.Experiment))
}

// runCanarySchedule runs a canary from the daemon. The result carries the
// spend of every look (confirmations included) so the daemon's ledger
// counts it; the canary sends its own alerts, so the result names no
// verdict decision.
func runCanarySchedule(ctx context.Context, a *app, s schedule.Schedule) (*schedule.Result, error) {
	d, err := canary.Load(schedule.Resolve(a.cfg.Root, s.Experiment))
	if err != nil {
		return nil, err
	}
	recs, err := newCanaryRunner(a).Run(ctx, d, canary.RunOptions{})
	res := &schedule.Result{}
	for _, r := range recs {
		res.CostUSD += r.CostUSD
		res.Runs += r.Runs
		res.RunErrors += r.RunErrors
		if r.ExpID != "" {
			res.ExpID = r.ExpID
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

// canarySource serves /api/bench/canaries (handlers.CanarySource). Start
// runs a look in the background with a fresh app on the live gateway; a
// canary already running answers busy.
type canarySource struct {
	root  string // project root
	state string // canaries' state folder
	start func(name string) error
	mu    *sync.Mutex
}

// List implements handlers.CanarySource.
func (c canarySource) List(_ context.Context) (*canary.List, error) {
	defs, errs := canary.LoadAll(filepath.Join(c.root, canary.DefaultDir))
	return canary.BuildList(c.state, defs, errs, time.Now())
}

// Get implements handlers.CanarySource.
func (c canarySource) Get(_ context.Context, name string) (*canary.Detail, error) {
	if !canary.ValidName(name) {
		return nil, fmt.Errorf("canary %q: %w", name, schemas.ErrNotFound)
	}
	p := canary.Path(c.root, name)
	if _, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("canary %q: %w", name, schemas.ErrNotFound)
	}
	d, err := canary.Load(p)
	if err != nil {
		return nil, err
	}
	return canary.BuildDetail(c.state, d, time.Now())
}

// Start implements handlers.CanarySource.
func (c canarySource) Start(ctx context.Context, name string) (*canary.RunAccepted, error) {
	if _, err := c.Get(ctx, name); err != nil {
		return nil, err
	}
	if c.start == nil {
		return nil, canary.ErrOffline
	}
	if err := c.start(name); err != nil {
		return nil, err
	}
	return &canary.RunAccepted{Canary: name, Started: time.Now().UTC(), Message: "canary look started in the background; poll GET /api/bench/canaries/" + name}, nil
}

// serveCanaries builds the serve source; offline servers list and show but
// cannot start a look.
func serveCanaries(a *app, offline bool) canarySource {
	src := canarySource{root: a.cfg.Root, state: filepath.Join(a.store.Root(), canaryStateDir), mu: &sync.Mutex{}}
	if offline {
		return src
	}
	running := map[string]bool{}
	src.start = func(name string) error {
		src.mu.Lock()
		if running[name] {
			src.mu.Unlock()
			return fmt.Errorf("canary %s: %w", name, canary.ErrBusy)
		}
		running[name] = true
		src.mu.Unlock()
		d, err := canary.Load(canary.Path(a.cfg.Root, name))
		if err != nil {
			src.mu.Lock()
			delete(running, name)
			src.mu.Unlock()
			return err
		}
		go func() {
			defer func() {
				src.mu.Lock()
				delete(running, name)
				src.mu.Unlock()
			}()
			if _, err := newCanaryRunner(a).Run(context.Background(), d, canary.RunOptions{}); err != nil {
				a.logger.Warn("canary look from the API ended with an error", "canary", name, "error", err.Error())
			}
		}()
		return nil
	}
	return src
}
