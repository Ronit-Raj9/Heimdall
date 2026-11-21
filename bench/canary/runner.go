package canary

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ErrBusy reports a canary already running in this or another process.
var ErrBusy = errors.New("canary is already running")

// ErrOffline reports a server that serves canaries but cannot launch a look
// (serve -offline: no gateway).
var ErrOffline = errors.New("this server cannot launch canary looks (offline: no gateway)")

// ErrNoBaseline reports a run asked for before any baseline was pinned.
var ErrNoBaseline = errors.New("no baseline pinned: run `canary baseline <name>` first")

// recentCosts is how many recent records the canary's own cost forecast
// reads.
const recentCosts = 5

// Plan is one canary run resolved by the Executor, not yet frozen.
type Plan struct {
	ExpID        string
	ManifestHash string
	Fingerprint  string
	Slots        int
	ForecastUSD  *float64 // high end of bench/forecast for the plan; nil when it has no basis
	ForecastNote string
	Rename       map[string]string
	Manifest     any // the planned manifest, handed back to Execute
}

// Executor plans, runs and reads back canary experiments;
// cmd/bifrost-bench implements it with the engine and the run store.
type Executor interface {
	// Plan resolves the definition into a manifest without running
	// anything. inject, when set, replaces the watched condition with a
	// probe (smoke canaries only).
	Plan(ctx context.Context, d *Definition, inject string) (*Plan, error)
	// Execute freezes and runs a plan and returns the experiment id and its
	// spend, unknown costs counted at the engine's estimate. After the
	// freeze, a failure still returns the id and the spend so far.
	Execute(ctx context.Context, p *Plan) (expID string, costUSD float64, err error)
	// Observe reads an experiment back from the sealed run folder, every run
	// checked against its seal. rename maps an injected condition back.
	Observe(ctx context.Context, expID string, rename map[string]string) (*Execution, error)
}

// Notifier sends alerts; *alerts.Sender implements it.
type Notifier interface {
	Send(ctx context.Context, cfgs []alerts.Config, p alerts.Payload) error
}

// Runner runs canaries: baseline, look and confirmation, with the caps, the
// state machine and the alerts.
type Runner struct {
	Root        string // state folder: <runs root>/.canaries
	Exec        Executor
	Notifier    Notifier        // nil sends no alerts
	ExtraAlerts []alerts.Config // receivers for every canary (bench.yaml alerts)
	Now         func() time.Time
	Logger      schemas.Logger
}

// RunOptions tune one `canary run`.
type RunOptions struct {
	// Inject forces a probe on a smoke canary (a test hook that makes a
	// drift at zero cost); refused on any other.
	Inject string
	// NoConfirm skips the immediate confirmation look after a flag.
	NoConfirm bool
	// Allow, when set, is asked before each look with what this Run's
	// earlier looks cost and returns why the look may not start, or "". The
	// schedule daemon sets it so a confirmation look fits the schedule's
	// daily cap too, not only the canary's own (QC2-15-04).
	Allow func(spentUSD float64) string
}

// BaselineOptions tune `canary baseline`.
type BaselineOptions struct {
	From string // pin this existing experiment instead of running a new one
	Note string
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) logger() schemas.Logger {
	if r.Logger == nil {
		return schemas.NoopLogger{}
	}
	return r.Logger
}

// Run takes one look against the current baseline and, when it flags from
// a steady state, an immediate confirmation look. It returns the records it
// appended, newest last.
func (r *Runner) Run(ctx context.Context, d *Definition, opts RunOptions) ([]Record, error) {
	if opts.Inject != "" && !d.Smoke {
		return nil, fmt.Errorf("canary %s: -inject works only on a smoke canary (it forces a fault to test the alarm path)", d.Name)
	}
	unlock, err := r.lock(d.Name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	base, err := CurrentBaseline(r.Root, d.Name)
	if err != nil {
		return nil, err
	}
	if base == nil {
		return nil, ErrNoBaseline
	}
	rec, err := r.look(ctx, d, base, KindRun, opts.Inject, opts.allow(0))
	out := []Record{rec}
	if err != nil || opts.NoConfirm || rec.Outcome != OutcomeFlagged || rec.State != StateWatching {
		return out, err
	}
	r.logger().Info("canary flagged; taking a confirmation look now", "canary", d.Name, "flagged", strings.Join(rec.Comparison.Flagged, ", "))
	conf, err := r.look(ctx, d, base, KindConfirmation, opts.Inject, opts.allow(rec.CostUSD))
	return append(out, conf), err
}

// allow binds Allow to what the earlier looks spent; nil without Allow.
func (o RunOptions) allow(spent float64) func() string {
	if o.Allow == nil {
		return nil
	}
	return func() string { return o.Allow(spent) }
}

// Baseline pins a known-good baseline: a fresh run of the frozen config, or
// an existing experiment of the same fingerprint. An invalid run is never
// pinned. Earlier baselines stay in the history.
func (r *Runner) Baseline(ctx context.Context, d *Definition, opts BaselineOptions) (*Record, error) {
	unlock, err := r.lock(d.Name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	records, err := ReadRecords(r.Root, d.Name)
	if err != nil {
		return nil, err
	}
	rec := Record{ID: "r_" + r.now().UTC().Format("20060102T150405.000"), Canary: d.Name, Kind: KindBaseline, Started: r.now(), Smoke: d.Smoke}
	finish := func(outcome, reason string, events ...string) (*Record, error) {
		rec.Outcome, rec.Reason, rec.Finished = outcome, reason, r.now()
		if rec.State == "" {
			rec.State = currentState(records)
		}
		r.alert(ctx, d, &rec, records, events)
		if err := appendRecord(r.Root, rec); err != nil {
			return &rec, err
		}
		if outcome != OutcomePinned {
			return &rec, fmt.Errorf("canary %s baseline: %s", d.Name, reason)
		}
		return &rec, nil
	}
	if opts.From == "" {
		if reason := r.capRefusal(d, records); reason != "" {
			return finish(OutcomeRefused, reason, alerts.EventScheduleFailed)
		}
	}
	plan, err := r.Exec.Plan(ctx, d, "")
	if err != nil {
		return finish(OutcomeError, "plan: "+err.Error(), alerts.EventScheduleFailed)
	}
	var x *Execution
	if opts.From != "" {
		if x, err = r.Exec.Observe(ctx, opts.From, nil); err != nil {
			return finish(OutcomeError, "read "+opts.From+": "+err.Error())
		}
		if x.Fingerprint != plan.Fingerprint {
			return finish(OutcomeStale, fmt.Sprintf("experiment %s was frozen from another config (fingerprint %s, the definition plans %s); pin a run of this definition", opts.From, short(x.Fingerprint), short(plan.Fingerprint)))
		}
	} else {
		if reason := r.forecastRefusal(d, records, plan); reason != "" {
			return finish(OutcomeRefused, reason, alerts.EventScheduleFailed)
		}
		expID, cost, err := r.Exec.Execute(ctx, plan)
		rec.ExpID, rec.CostUSD = expID, cost
		if err != nil {
			return finish(OutcomeError, "run: "+err.Error(), alerts.EventScheduleFailed)
		}
		if x, err = r.Exec.Observe(ctx, expID, nil); err != nil {
			return finish(OutcomeError, "read back "+expID+": "+err.Error(), alerts.EventScheduleFailed)
		}
	}
	rec.ID, rec.ExpID, rec.ManifestHash, rec.Fingerprint = x.ExpID, x.ExpID, x.ManifestHash, x.Fingerprint
	rec.Served, rec.Points, rec.Runs, rec.RunErrors = ServedSummary(x), points(x), len(x.Runs), runErrors(x)
	if inv := Invalid(x); len(inv) > 0 {
		rec.Invalid = inv
		rec.State = currentState(records)
		return finish(OutcomeInvalid, "not pinned: the baseline run is invalid: "+strings.Join(inv, "; "), alerts.EventExperimentInvalid)
	}
	b := Baseline{ExpID: x.ExpID, ManifestHash: x.ManifestHash, Fingerprint: x.Fingerprint, PinnedAt: r.now(), CreatedAt: x.CreatedAt,
		Served: ServedSummary(x), Clusters: []string{}, Smoke: x.Smoke, Note: opts.Note}
	for _, c := range x.Clusters {
		b.Clusters = append(b.Clusters, c.Signature)
	}
	if err := appendBaseline(r.Root, d.Name, b); err != nil {
		return nil, err
	}
	rec.Baseline, rec.State = x.ExpID, StateSteady
	return finish(OutcomePinned, fmt.Sprintf("baseline pinned: %s (%d runs, success %s)", x.ExpID, len(x.Runs), successText(rec.Points)))
}

// look is one comparison against the baseline.
func (r *Runner) look(ctx context.Context, d *Definition, base *Baseline, kind, inject string, allow func() string) (Record, error) {
	records, err := ReadRecords(r.Root, d.Name)
	if err != nil {
		return Record{}, err
	}
	rec := Record{ID: "r_" + r.now().UTC().Format("20060102T150405.000"), Canary: d.Name, Kind: kind, Started: r.now(),
		Baseline: base.ExpID, Injected: inject, Smoke: d.Smoke}
	prevState := currentState(records)
	finish := func(outcome, state, reason string, events ...string) (Record, error) {
		rec.Outcome, rec.State, rec.Reason, rec.Finished = outcome, state, reason, r.now()
		r.alert(ctx, d, &rec, records, events)
		if err := appendRecord(r.Root, rec); err != nil {
			return rec, err
		}
		return rec, nil
	}
	deferred := func(reason string) string {
		if kind == KindConfirmation {
			return "confirmation deferred: " + reason + "; the next scheduled look confirms or clears the flag"
		}
		return reason
	}
	// The daily cap is checked before anything is planned.
	if reason := r.capRefusal(d, records); reason != "" {
		return finish(OutcomeRefused, prevState, deferred(reason), alerts.EventScheduleFailed)
	}
	if allow != nil {
		if reason := allow(); reason != "" {
			return finish(OutcomeRefused, prevState, deferred(reason), alerts.EventScheduleFailed)
		}
	}
	plan, err := r.Exec.Plan(ctx, d, inject)
	if err != nil {
		return finish(OutcomeError, prevState, "plan: "+err.Error(), alerts.EventScheduleFailed)
	}
	if plan.Fingerprint != base.Fingerprint {
		return finish(OutcomeStale, StateBaselineStale, fmt.Sprintf("the frozen config changed since the baseline (fingerprint %s, baseline %s): the agent config, its image, the suite lock or the task selection moved; nothing was run. Re-pin with `canary baseline %s` once the change is intended",
			short(plan.Fingerprint), short(base.Fingerprint), d.Name))
	}
	if reason := r.forecastRefusal(d, records, plan); reason != "" {
		return finish(OutcomeRefused, prevState, deferred(reason), alerts.EventScheduleFailed)
	}
	rec.ForecastUSD = plan.ForecastUSD
	expID, cost, err := r.Exec.Execute(ctx, plan)
	rec.ID, rec.ExpID, rec.CostUSD = cmpOr(expID, rec.ID), expID, cost
	if err != nil {
		return finish(OutcomeError, prevState, "run: "+err.Error(), alerts.EventScheduleFailed)
	}
	cur, err := r.Exec.Observe(ctx, expID, plan.Rename)
	if err != nil {
		return finish(OutcomeError, prevState, "read back "+expID+": "+err.Error(), alerts.EventScheduleFailed)
	}
	cur.Injected = inject
	rec.ManifestHash, rec.Fingerprint = cur.ManifestHash, cur.Fingerprint
	rec.Served, rec.Points, rec.Runs, rec.RunErrors = ServedSummary(cur), points(cur), len(cur.Runs), runErrors(cur)
	bx, err := r.Exec.Observe(ctx, base.ExpID, nil)
	var inv []string
	switch {
	case err != nil:
		inv = append(inv, "baseline "+base.ExpID+" cannot be read: "+err.Error())
	case bx.ManifestHash != base.ManifestHash:
		inv = append(inv, fmt.Sprintf("baseline %s manifest hash %s differs from the pinned %s", base.ExpID, short(bx.ManifestHash), short(base.ManifestHash)))
	default:
		for _, s := range Invalid(bx) {
			inv = append(inv, "baseline: "+s)
		}
	}
	inv = append(inv, Invalid(cur)...)
	if len(inv) > 0 {
		rec.Invalid = inv
		return finish(OutcomeInvalid, StateInvalid, "not compared: "+strings.Join(inv, "; "), alerts.EventExperimentInvalid)
	}
	cmp := Compare(bx.Runs, cur.Runs, cur.Arms, Metrics, d.Alpha(), d.Floor)
	rec.Comparison = &cmp
	rec.Changes = WhatChanged(bx, cur, &cmp, base.Clusters)
	rec.Equivalence = CompareProviders(d, cur, d.Alpha())
	outcome := OutcomeClear
	if len(cmp.Flagged) > 0 {
		outcome = OutcomeFlagged
	}
	var prevLookState string
	var prevFlagged []string
	if pl := lastLook(records, base); pl != nil {
		prevLookState = pl.State
		if pl.Comparison != nil {
			prevFlagged = pl.Comparison.Flagged
		}
	}
	state := NextState(prevLookState, prevFlagged, outcome, cmp.Flagged)
	var events []string
	reason := ""
	switch state {
	case StateDrifted:
		reason = "drift confirmed by two consecutive looks on " + strings.Join(Overlap(cmp.Flagged, prevFlagged), ", ")
		if prevState != StateDrifted {
			events = append(events, alerts.EventCanaryDrifted)
		}
	case StateWatching:
		reason = "flagged once on " + strings.Join(cmp.Flagged, ", ") + "; a second consecutive flag on the same target and metric confirms a drift"
	default:
		reason = "no drift detected"
		if age := r.now().Sub(base.CreatedAt); !base.CreatedAt.IsZero() && age > d.MaxBaselineAge() {
			state = StateBaselineStale
			reason = fmt.Sprintf("no drift detected, but the baseline is %d days old (max %d): re-pin it so slow changes are measured from a recent known-good run", int(age.Hours()/24), int(d.MaxBaselineAge().Hours()/24))
		}
	}
	if changed := ServedChangedSince(base, rec.Served); len(changed) > 0 && !servedChangeAlerted(records, base) {
		events = append(events, alerts.EventCanaryModelChanged)
	}
	return finish(outcome, state, reason, events...)
}

// capRefusal returns why a run may not start under the caps, or "". It
// runs before planning. The day's spend plus the whole per-run cap must fit
// the daily cap, because the engine may spend up to the cap before it stops
// leasing.
func (r *Runner) capRefusal(d *Definition, records []Record) string {
	if !finite(d.Budget.PerRunUSD) || !finite(d.Budget.PerDayUSD) { // QC2-15-09
		return "budget caps must be finite numbers"
	}
	if !d.Smoke && (d.Budget.PerRunUSD <= 0 || d.Budget.PerDayUSD <= 0) {
		return "budget.per_run_usd and budget.per_day_usd are required for a canary that can spend"
	}
	loc := d.Location()
	day := r.now().In(loc).Format(time.DateOnly)
	spent := SpentOn(records, day, loc)
	if d.Budget.PerDayUSD > 0 && spent+d.Budget.PerRunUSD > d.Budget.PerDayUSD+1e-9 {
		return fmt.Sprintf("daily cap: spent %.4f USD today plus the per-run cap %.2f USD would pass per_day_usd %.2f USD", spent, d.Budget.PerRunUSD, d.Budget.PerDayUSD)
	}
	return ""
}

// forecastRefusal returns why a planned run may not start: its forecast is
// above the per-run cap, so it would stop at the cap with slots unrun.
func (r *Runner) forecastRefusal(d *Definition, records []Record, plan *Plan) string {
	if d.Budget.PerRunUSD > 0 {
		f, basis := r.forecast(records, plan)
		if f > d.Budget.PerRunUSD+1e-9 {
			return fmt.Sprintf("forecast %.4f USD (%s) is above per_run_usd %.2f USD: the run would stop at its cap with slots unrun, an invalid look; raise the cap or shrink the canary", f, basis, d.Budget.PerRunUSD)
		}
	}
	return ""
}

// forecast is the larger of bench/forecast's high end for the plan and the
// most expensive of the canary's recent runs.
func (r *Runner) forecast(records []Record, plan *Plan) (float64, string) {
	f, basis := 0.0, "no history"
	if plan != nil && plan.ForecastUSD != nil {
		f, basis = *plan.ForecastUSD, "bench forecast high end"
		if plan.ForecastNote != "" {
			basis += ": " + plan.ForecastNote
		}
	}
	n := 0
	for i := len(records) - 1; i >= 0 && n < recentCosts; i-- {
		if records[i].ExpID == "" {
			continue
		}
		n++
		if records[i].CostUSD > f {
			f, basis = records[i].CostUSD, "the most expensive of the last runs of this canary"
		}
	}
	return f, basis
}

// alert sends a record's events to the canary's receivers and notes them.
func (r *Runner) alert(ctx context.Context, d *Definition, rec *Record, records []Record, events []string) {
	cfgs := append(slices.Clone(d.Alerts), r.ExtraAlerts...)
	if r.Notifier == nil || len(cfgs) == 0 {
		return
	}
	loc := d.Location()
	spent := SpentOn(append(slices.Clone(records), *rec), r.now().In(loc).Format(time.DateOnly), loc)
	for _, ev := range events {
		p := alerts.Payload{Event: ev, Schedule: d.ScheduleName(), Experiment: rec.ExpID, Decision: rec.State,
			Link: "/canaries?name=" + d.Name, Canary: d.Name, Message: alertMessage(d, rec, ev),
			Summary: alerts.Summary{Runs: rec.Runs, RunErrors: rec.RunErrors, CostUSD: rec.CostUSD, SpentTodayUSD: spent, MaxUSDPerDay: d.Budget.PerDayUSD}}
		if rec.Changes != nil {
			p.Changes = rec.Changes.Moved
		}
		if ev == alerts.EventCanaryModelChanged {
			p.Changes = ServedChangedSinceRecord(rec)
		}
		if err := r.Notifier.Send(context.WithoutCancel(ctx), cfgs, p); err != nil {
			r.logger().Warn("canary alert not delivered", "canary", d.Name, "event", ev, "error", err.Error())
		}
		rec.Alerts = append(rec.Alerts, ev)
	}
}

// ServedChangedSinceRecord lists the served-model lines of a record's
// changes.
func ServedChangedSinceRecord(rec *Record) []string {
	var out []string
	if rec.Changes == nil {
		return out
	}
	for _, a := range rec.Changes.Arms {
		if a.ServedChanged {
			out = append(out, fmt.Sprintf("%s: %s -> %s", a.Label, joinOr(a.ServedBefore, "none"), joinOr(a.ServedNow, "none")))
		}
	}
	return out
}

// alertMessage is the one-line text of an alert.
func alertMessage(d *Definition, rec *Record, ev string) string {
	head := fmt.Sprintf("canary %s %s", d.Name, rec.State)
	if d.Smoke || rec.Injected != "" {
		head += " (smoke canary: synthetic, never a finding)"
	}
	var moved []string
	if rec.Changes != nil {
		moved = rec.Changes.Moved
	}
	switch ev {
	case alerts.EventCanaryDrifted:
		if len(moved) > 3 {
			moved = moved[:3]
		}
		return head + ": " + strings.Join(moved, "; ")
	case alerts.EventCanaryModelChanged:
		return head + ": served model changed under the same alias: " + strings.Join(ServedChangedSinceRecord(rec), "; ")
	}
	return head + ": " + rec.Reason
}

// servedChangeAlerted reports whether a look since the baseline already
// alerted a served-model change.
func servedChangeAlerted(records []Record, base *Baseline) bool {
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Kind == KindBaseline && r.Outcome != OutcomePinned {
			continue
		}
		if r.Kind == KindBaseline || r.Baseline != base.ExpID {
			return false
		}
		if slices.Contains(r.Alerts, alerts.EventCanaryModelChanged) {
			return true
		}
	}
	return false
}

// currentState is the state after the newest record, no_baseline with none.
func currentState(records []Record) string {
	if len(records) == 0 {
		return StateNoBaseline
	}
	return records[len(records)-1].State
}

// points are the per-arm headline numbers of an execution.
func points(x *Execution) []ArmPoint {
	means := taskMeans(x.Runs, MetricSuccess)
	byArm := map[string][]float64{}
	runs := map[string]int{}
	for k, v := range means {
		byArm[k.arm] = append(byArm[k.arm], v)
	}
	for _, r := range x.Runs {
		runs[r.Arm]++
	}
	var out []ArmPoint
	for _, arm := range sortedKeys(x.Arms) {
		p := ArmPoint{Arm: arm, Tasks: len(byArm[arm]), Runs: runs[arm]}
		if xs := byArm[arm]; len(xs) > 0 {
			s := 0.0
			for _, v := range xs {
				s += v
			}
			m := s / float64(len(xs))
			p.Success = &m
		}
		out = append(out, p)
	}
	return out
}

// runErrors counts runs that ended in an infrastructure error.
func runErrors(x *Execution) int {
	n := 0
	for _, r := range x.Runs {
		if r.InfraError {
			n++
		}
	}
	return n
}

// successText formats the points' success.
func successText(ps []ArmPoint) string {
	var out []string
	for _, p := range ps {
		if p.Success != nil {
			out = append(out, fmt.Sprintf("%s %.0f%%", p.Arm, 100**p.Success))
		}
	}
	return joinOr(out, "unknown")
}

// Location is the timezone of the canary's schedule and spend day.
func (d *Definition) Location() *time.Location {
	if d.Schedule != nil && d.Schedule.Timezone != "" {
		if loc, err := time.LoadLocation(d.Schedule.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

// short shortens a hash for messages.
func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// lock takes the canary's lock file without waiting.
func (r *Runner) lock(name string) (func(), error) {
	dir := stateDir(r.Root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("canary state folder: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("canary lock: %w", err)
	}
	unlock, err := tryLock(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		if err := unlock(); err != nil {
			r.logger().Warn("release canary lock", "canary", name, "error", err.Error())
		}
	}, nil
}
