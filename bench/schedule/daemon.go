package schedule

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/schemas"
)

// State files, under the daemon's state folder (the CLI uses
// <runs root>/.schedules, which the run store never lists as an experiment).
const (
	SpendFile      = "spend.json"       // schedule -> day -> USD spent by scheduled runs
	RecordsFile    = "runs.jsonl"       // one Record per trigger
	DeliveriesFile = "deliveries.jsonl" // the alert delivery log (package alerts)
	spendLockFile  = "spend.lock"
)

// Record statuses.
const (
	StatusOK      = "ok"      // ran; the verdict is not in fail_on and no policy blocked
	StatusFailed  = "failed"  // ran; the verdict is in fail_on or the release policy blocked
	StatusError   = "error"   // the run could not finish (plan, run or analysis failed)
	StatusRefused = "refused" // not started: the daily cap would be passed, or the experiment cannot be read
	StatusSkipped = "skipped" // not started: the same schedule is still running
)

// spendKeepDays is how long the spend ledger keeps a day.
const spendKeepDays = 40

// errBusy reports a schedule lock held by another process.
var errBusy = errors.New("schedule is running in another process")

// Clock is the daemon's time source, replaced by a fake clock in tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Result is what one scheduled run produced. A Runner may return a Result
// with an error when the experiment was frozen but did not finish, so the
// spend it made still counts against the day.
type Result struct {
	ExpID string `json:"exp_id,omitempty"`
	// EvaluationID is the evaluation an evaluation schedule started. It
	// runs on after the trigger ends; its gate is judged when it finishes.
	EvaluationID string  `json:"evaluation_id,omitempty"`
	AnalysisID   string  `json:"analysis_id,omitempty"`
	Decision     string  `json:"decision,omitempty"` // verdict decision
	Action       string  `json:"action,omitempty"`   // verdict action
	Policy       string  `json:"policy,omitempty"`   // release policy decision; "" with no policy
	CostUSD      float64 `json:"cost_usd"`           // spend of this run, unknown costs counted at the engine's estimate
	Runs         int     `json:"runs"`               // sealed counted runs
	RunErrors    int     `json:"run_errors"`         // runs that ended infra_error or aborted_infra
	Summary      string  `json:"summary,omitempty"`
}

// Runner plans, runs and checks one schedule's experiment.
type Runner interface {
	Run(ctx context.Context, s Schedule) (*Result, error)
}

// Notifier sends alerts; *alerts.Sender implements it.
type Notifier interface {
	Send(ctx context.Context, cfgs []alerts.Config, p alerts.Payload) error
}

// Record is one trigger of a schedule, appended to runs.jsonl.
type Record struct {
	At            time.Time `json:"at"`       // when the trigger was due
	Started       time.Time `json:"started"`  // when the daemon took it up
	Finished      time.Time `json:"finished"` // when it ended
	Schedule      string    `json:"schedule"`
	Status        string    `json:"status"`
	Reason        string    `json:"reason,omitempty"`
	Result        *Result   `json:"result,omitempty"`
	SpentTodayUSD float64   `json:"spent_today_usd"`
	Alerts        []string  `json:"alerts,omitempty"` // events alerted
}

// Options configure a Daemon.
type Options struct {
	Root        string           // project root that schedule paths resolve against
	StateDir    string           // spend ledger, trigger records and delivery log
	Runner      Runner           // required
	Notifier    Notifier         // nil sends no alerts
	Load        ExperimentLoader // required: reads an experiment file for its spend cap
	Clock       Clock            // nil is the real clock
	Logger      schemas.Logger   // nil discards
	ExtraAlerts []alerts.Config  // receivers for every schedule, from the server config
}

// Daemon triggers schedules at their cron times. Triggers run one at a
// time, so two schedules never run together and one schedule never overlaps
// itself; times missed while another run was going are skipped, not queued.
type Daemon struct {
	file     *File
	o        Options
	mu       sync.Mutex
	running  map[string]bool
	ledgerMu sync.Mutex
}

// entry is one schedule with its parsed timetable.
type entry struct {
	s    Schedule
	cron *Cron
	loc  *time.Location
	next time.Time
}

// New returns a daemon over f. It checks the cron expressions and timezones;
// the caller validates the files (File.Validate) beforehand.
func New(f *File, o Options) (*Daemon, error) {
	if f == nil || o.Runner == nil || o.Load == nil {
		return nil, errors.New("new schedule daemon: file, runner and experiment loader are required")
	}
	if o.StateDir == "" {
		return nil, errors.New("new schedule daemon: state folder is empty")
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Logger == nil {
		o.Logger = schemas.NoopLogger{}
	}
	for _, s := range f.Schedules {
		if _, err := ParseCron(s.Cron); err != nil {
			return nil, fmt.Errorf("schedule %s: %w", s.Name, err)
		}
		if _, err := s.Location(); err != nil {
			return nil, fmt.Errorf("schedule %s: %w", s.Name, err)
		}
	}
	return &Daemon{file: f, o: o, running: map[string]bool{}}, nil
}

// Run triggers schedules until ctx ends. It returns nil when ctx ends.
func (d *Daemon) Run(ctx context.Context) error {
	entries, err := d.entries()
	if err != nil {
		return err
	}
	for _, e := range entries {
		d.o.Logger.Info("schedule armed", "schedule", e.s.Name, "cron", e.cron.String(), "next", e.next.Format(time.RFC3339))
	}
	for {
		earliest := time.Time{}
		for _, e := range entries {
			if !e.next.IsZero() && (earliest.IsZero() || e.next.Before(earliest)) {
				earliest = e.next
			}
		}
		if earliest.IsZero() {
			<-ctx.Done()
			return nil
		}
		if wait := earliest.Sub(d.o.Clock.Now()); wait > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-d.o.Clock.After(wait):
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		now := d.o.Clock.Now()
		var due []*entry
		for _, e := range entries {
			if !e.next.IsZero() && !e.next.After(now) {
				due = append(due, e)
			}
		}
		slices.SortFunc(due, func(a, b *entry) int {
			if c := a.next.Compare(b.next); c != 0 {
				return c
			}
			return cmp.Compare(a.s.Name, b.s.Name)
		})
		for _, e := range due {
			if ctx.Err() != nil {
				return nil
			}
			d.trigger(ctx, e.s, e.next)
			d.advance(e)
		}
	}
}

// Trigger runs one schedule now, as `schedule run-now` does, with the same
// checks as a timed trigger: no overlap with itself, the daily cap, alerts.
func (d *Daemon) Trigger(ctx context.Context, name string) (Record, error) {
	s, ok := d.file.Find(name)
	if !ok {
		return Record{}, fmt.Errorf("schedule %q: %w", name, schemas.ErrNotFound)
	}
	return d.trigger(ctx, s, d.o.Clock.Now()), nil
}

// entries parses every schedule and computes its first time.
func (d *Daemon) entries() ([]*entry, error) {
	now := d.o.Clock.Now()
	out := make([]*entry, 0, len(d.file.Schedules))
	for _, s := range d.file.Schedules {
		c, err := ParseCron(s.Cron)
		if err != nil {
			return nil, fmt.Errorf("schedule %s: %w", s.Name, err)
		}
		loc, err := s.Location()
		if err != nil {
			return nil, fmt.Errorf("schedule %s: %w", s.Name, err)
		}
		out = append(out, &entry{s: s, cron: c, loc: loc, next: c.Next(now.In(loc))})
	}
	return out, nil
}

// advance moves e to its first time after now and logs the times a long run
// made it miss.
func (d *Daemon) advance(e *entry) {
	now := d.o.Clock.Now().In(e.loc)
	missed := 0
	for t := e.cron.Next(e.next.In(e.loc)); !t.IsZero() && !t.After(now) && missed < 1000; t = e.cron.Next(t) {
		missed++
	}
	if missed > 0 {
		d.o.Logger.Warn("schedule times missed while a run was going; they are skipped, not queued", "schedule", e.s.Name, "missed", missed)
	}
	e.next = e.cron.Next(now)
}

// trigger runs one schedule once and records the outcome.
func (d *Daemon) trigger(ctx context.Context, s Schedule, at time.Time) Record {
	rec := Record{At: at, Started: d.o.Clock.Now(), Schedule: s.Name}
	release, err := d.acquire(s.Name)
	if err != nil {
		rec.Status, rec.Reason = StatusSkipped, err.Error()
		d.o.Logger.Warn("schedule trigger skipped", "schedule", s.Name, "reason", rec.Reason)
		return d.finish(ctx, s, rec, nil)
	}
	defer func() {
		if err := release(); err != nil {
			d.o.Logger.Warn("release schedule lock", "schedule", s.Name, "error", err.Error())
		}
	}()
	loc, err := s.Location()
	if err != nil {
		rec.Status, rec.Reason = StatusRefused, err.Error()
		return d.finish(ctx, s, rec, []string{alerts.EventScheduleFailed})
	}
	day := rec.Started.In(loc).Format(time.DateOnly)
	approval, reason := d.approve(s, day)
	if reason != "" {
		rec.Status, rec.Reason = StatusRefused, reason
		rec.SpentTodayUSD = d.spent(s.Name, day)
		d.o.Logger.Warn("schedule run refused", "schedule", s.Name, "reason", reason)
		return d.finish(ctx, s, rec, []string{alerts.EventScheduleFailed})
	}
	// The approved cap is reserved in the ledger before the run starts, so
	// a crash or a failed settlement after spending never frees it
	// (QC2-15-10); the settlement below replaces it with the run's cost.
	var reserved float64
	if approval != nil && approval.Limited {
		if _, err := d.addAmount(s.Name, day, approval.CapUSD); err != nil {
			rec.Status, rec.Reason = StatusRefused, "spend ledger: the run's reservation could not be written: "+err.Error()
			d.o.Logger.Error("schedule run refused", "schedule", s.Name, "reason", rec.Reason)
			return d.finish(ctx, s, rec, []string{alerts.EventScheduleFailed})
		}
		reserved = approval.CapUSD
		approval.Reserve = func(usd float64) string {
			if !(usd > 0) || math.IsInf(usd, 0) {
				return fmt.Sprintf("reservation %v USD is not a finite, positive amount", usd)
			}
			if spent := d.spent(s.Name, day); spent+usd > s.MaxUSDPerDay+1e-9 {
				return fmt.Sprintf("daily cap: spent %.4f USD today (reservations included) plus %.2f USD would pass max_usd_per_day %.2f USD", spent, usd, s.MaxUSDPerDay)
			}
			if _, err := d.addAmount(s.Name, day, usd); err != nil {
				return "spend ledger: the reservation could not be written: " + err.Error()
			}
			reserved += usd
			return ""
		}
	}
	s.Approved = approval
	d.o.Logger.Info("schedule run starting", "schedule", s.Name, "experiment", s.Experiment)
	res, runErr := d.o.Runner.Run(ctx, s)
	rec.Result = res
	cost := 0.0
	if res != nil && res.CostUSD > 0 && !math.IsInf(res.CostUSD, 0) {
		cost = res.CostUSD
	}
	spent, lerr := d.addAmount(s.Name, day, cost-reserved)
	if lerr != nil {
		d.o.Logger.Error("spend ledger not settled; the run's reservation stays counted", "schedule", s.Name, "error", lerr.Error())
		spent = d.spent(s.Name, day)
	}
	rec.SpentTodayUSD = spent
	rec.Status = StatusOK
	if runErr != nil {
		rec.Status, rec.Reason = StatusError, runErr.Error()
	} else if failedCheck(s, res) {
		rec.Status = StatusFailed
	}
	return d.finish(ctx, s, rec, EventsFor(res, runErr))
}

// finish sends the alerts and appends the record.
func (d *Daemon) finish(ctx context.Context, s Schedule, rec Record, events []string) Record {
	rec.Finished = d.o.Clock.Now()
	cfgs := d.file.AlertsFor(s, d.o.ExtraAlerts)
	if d.o.Notifier != nil && len(cfgs) > 0 {
		for _, ev := range events {
			if err := d.o.Notifier.Send(context.WithoutCancel(ctx), cfgs, payload(ev, s, rec)); err != nil {
				d.o.Logger.Warn("alert not delivered", "schedule", s.Name, "event", ev, "error", err.Error())
			}
			rec.Alerts = append(rec.Alerts, ev)
		}
	}
	if err := appendRecord(filepath.Join(d.o.StateDir, RecordsFile), rec); err != nil {
		d.o.Logger.Error("schedule record not written", "schedule", s.Name, "error", err.Error())
	}
	return rec
}

// acquire marks a schedule running in this process and takes its lock file,
// so a run-now in another process cannot overlap a running trigger either.
func (d *Daemon) acquire(name string) (func() error, error) {
	d.mu.Lock()
	if d.running[name] {
		d.mu.Unlock()
		return nil, errors.New("schedule is still running")
	}
	d.running[name] = true
	d.mu.Unlock()
	done := func() {
		d.mu.Lock()
		delete(d.running, name)
		d.mu.Unlock()
	}
	if err := os.MkdirAll(d.o.StateDir, 0o755); err != nil {
		done()
		return nil, fmt.Errorf("state folder: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(d.o.StateDir, name+".lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		done()
		return nil, fmt.Errorf("lock file: %w", err)
	}
	unlock, err := tryLock(f)
	if err != nil {
		done()
		return nil, errors.Join(err, f.Close())
	}
	return func() error {
		defer done()
		return unlock()
	}, nil
}

// capRefusal returns why a run may not start under the daily cap, or "".
// The check is conservative: today's spend plus the experiment's whole spend
// cap must fit under max_usd_per_day, because the engine may spend up to the
// cap (and counts unknown costs at its estimate) before it stops leasing.
func (d *Daemon) capRefusal(s Schedule, day string) string {
	_, reason := d.approve(s, day)
	return reason
}

// approve is capRefusal that also returns what it approved, for the Runner
// to check the experiment it runs against (Schedule.Approved). A built-in
// job is approved with nil.
func (d *Daemon) approve(s Schedule, day string) (*Approval, string) {
	if s.Job != "" {
		return nil, "" // a built-in job spends nothing
	}
	if math.IsNaN(s.MaxUSDPerDay) || math.IsInf(s.MaxUSDPerDay, 0) || s.MaxUSDPerDay < 0 { // QC2-15-09
		return nil, fmt.Sprintf("max_usd_per_day %v is not a finite, non-negative number", s.MaxUSDPerDay)
	}
	if s.Evaluation != "" {
		return d.approveEvaluation(s, day)
	}
	exp, err := d.o.Load(Resolve(d.o.Root, s.Experiment))
	if err != nil {
		return nil, "experiment file: " + err.Error()
	}
	if s.MaxUSDPerDay <= 0 {
		if exp.Smoke {
			return &Approval{}, ""
		}
		return nil, "max_usd_per_day is required for a non-smoke experiment"
	}
	if !finitePositive(exp.Budget.TotalUSD) {
		// A stated daily cap holds for smoke experiments too: smoke does not
		// prove every part (model-played users, judges) is scripted.
		return nil, "the experiment has no spend cap (budget.total_usd), so max_usd_per_day cannot hold"
	}
	spent := d.spent(s.Name, day)
	if spent+exp.Budget.TotalUSD > s.MaxUSDPerDay+1e-9 {
		return nil, fmt.Sprintf("daily cap: spent %.4f USD today plus the experiment's spend cap %.2f USD would pass max_usd_per_day %.2f USD",
			spent, exp.Budget.TotalUSD, s.MaxUSDPerDay)
	}
	return &Approval{CapUSD: exp.Budget.TotalUSD, Limited: true}, ""
}

// EventsFor returns the alert events a run's outcome calls for, in the
// order alerts.Events lists them.
func EventsFor(res *Result, runErr error) []string {
	var out []string
	if res != nil {
		if res.Decision == string(schemas.DecisionRegressed) {
			out = append(out, alerts.EventVerdictRegressed)
		}
		if res.Policy == string(schemas.PolicyBlock) {
			out = append(out, alerts.EventPolicyBlock)
		}
		if res.Decision == string(schemas.DecisionInvalid) {
			out = append(out, alerts.EventExperimentInvalid)
		}
		if res.RunErrors > 0 {
			out = append(out, alerts.EventRunErrors)
		}
	}
	if runErr != nil {
		out = append(out, alerts.EventScheduleFailed)
	}
	return out
}

// failedCheck reports whether a finished run fails the scheduled check.
func failedCheck(s Schedule, res *Result) bool {
	if res == nil {
		return false
	}
	return res.Policy == string(schemas.PolicyBlock) || slices.Contains(s.FailDecisions(), res.Decision)
}

// payload builds the alert for one event of a record.
func payload(event string, s Schedule, rec Record) alerts.Payload {
	p := alerts.Payload{Event: event, Schedule: s.Name, Message: rec.Reason,
		Summary: alerts.Summary{SpentTodayUSD: rec.SpentTodayUSD, MaxUSDPerDay: s.MaxUSDPerDay}}
	if r := rec.Result; r != nil {
		p.Experiment, p.Decision, p.Action, p.Policy = r.ExpID, r.Decision, r.Action, r.Policy
		p.Link = alerts.LinkPath(r.ExpID)
		if p.Link == "" && r.EvaluationID != "" {
			p.Link = "/evaluations/" + r.EvaluationID
		}
		p.Summary.Runs, p.Summary.RunErrors, p.Summary.CostUSD = r.Runs, r.RunErrors, r.CostUSD
		if p.Message == "" {
			p.Message = r.Summary
		}
	}
	if p.Message == "" {
		p.Message = "status " + rec.Status
	}
	return p
}

// spendLedger is spend.json: schedule -> day (YYYY-MM-DD in the schedule's
// timezone) -> USD.
type spendLedger map[string]map[string]float64

// spent returns a schedule's spend on day. A ledger that cannot be read
// counts as infinite spend, so a broken ledger refuses runs rather than
// letting them through.
func (d *Daemon) spent(name, day string) float64 {
	d.ledgerMu.Lock()
	defer d.ledgerMu.Unlock()
	l, err := readLedger(filepath.Join(d.o.StateDir, SpendFile))
	if err != nil {
		d.o.Logger.Error("spend ledger unreadable; runs are refused until it is fixed", "error", err.Error())
		return 1e18
	}
	return l[name][day]
}

// addAmount adds usd (a reservation, or a settlement that may be negative)
// to a schedule's day in the ledger and returns the day's total, never
// below zero.
func (d *Daemon) addAmount(name, day string, cost float64) (float64, error) {
	d.ledgerMu.Lock()
	defer d.ledgerMu.Unlock()
	unlock, err := d.lockLedger()
	if err != nil {
		return 0, err
	}
	p := filepath.Join(d.o.StateDir, SpendFile)
	l, err := readLedger(p)
	if err != nil {
		return 0, errors.Join(err, unlock())
	}
	if l[name] == nil {
		l[name] = map[string]float64{}
	}
	l[name][day] = max(l[name][day]+cost, 0)
	total := l[name][day]
	prune(l, d.o.Clock.Now())
	if err := writeJSONAtomic(p, l); err != nil {
		return total, errors.Join(err, unlock())
	}
	return total, unlock()
}

// lockLedger takes spend.lock, retrying briefly while another process
// holds it.
func (d *Daemon) lockLedger() (func() error, error) {
	if err := os.MkdirAll(d.o.StateDir, 0o755); err != nil {
		return nil, err
	}
	for i := 0; ; i++ {
		f, err := os.OpenFile(filepath.Join(d.o.StateDir, spendLockFile), os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return nil, err
		}
		unlock, err := tryLock(f)
		if err == nil {
			return unlock, nil
		}
		if cerr := f.Close(); cerr != nil || !errors.Is(err, errBusy) || i >= 50 {
			return nil, errors.Join(fmt.Errorf("lock spend ledger: %w", err), cerr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// readLedger reads spend.json; a missing file is an empty ledger.
func readLedger(p string) (spendLedger, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return spendLedger{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read spend ledger: %w", err)
	}
	l := spendLedger{}
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("parse spend ledger %s: %w", p, err)
	}
	for name, days := range l { // QC2-15-09: a persisted total that is not a finite amount refuses runs
		for day, v := range days {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return nil, fmt.Errorf("spend ledger %s: %s on %s is %v, not a finite, non-negative amount", p, name, day, v)
			}
		}
	}
	return l, nil
}

// prune drops days older than spendKeepDays.
func prune(l spendLedger, now time.Time) {
	cutoff := now.AddDate(0, 0, -spendKeepDays).Format(time.DateOnly)
	for name, days := range l {
		for day := range days {
			if day < cutoff {
				delete(days, day)
			}
		}
		if len(days) == 0 {
			delete(l, name)
		}
	}
}

// SpentToday returns a schedule's spend today (in its timezone) from the
// ledger in stateDir, for `schedule list`.
func SpentToday(stateDir string, s Schedule, now time.Time) (float64, error) {
	loc, err := s.Location()
	if err != nil {
		return 0, err
	}
	l, err := readLedger(filepath.Join(stateDir, SpendFile))
	if err != nil {
		return 0, err
	}
	return l[s.Name][now.In(loc).Format(time.DateOnly)], nil
}

// LastRecords returns the newest record of each schedule in stateDir.
func LastRecords(stateDir string) (map[string]Record, error) {
	f, err := os.Open(filepath.Join(stateDir, RecordsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Record{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read schedule records: %w", err)
	}
	defer f.Close()
	out := map[string]Record{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue // a torn last line from a crash is not fatal
		}
		out[r.Schedule] = r
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read schedule records: %w", err)
	}
	return out, nil
}

// appendRecord appends one JSON line.
func appendRecord(p string, rec Record) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// writeJSONAtomic writes v as indented JSON through a temporary file and a
// rename.
func writeJSONAtomic(p string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".spend-*.json")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(b, '\n'))
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}

// realClock is the wall clock.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
