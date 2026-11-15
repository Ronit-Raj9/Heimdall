package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeClock is a manual clock. Every After call signals waiting, so a test
// knows the daemon is blocked before it advances time.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
	waiting chan struct{}
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t, waiting: make(chan struct{}, 100)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	if d <= 0 {
		ch <- c.now
	} else {
		c.waiters = append(c.waiters, fakeWaiter{at: c.now.Add(d), ch: ch})
	}
	c.mu.Unlock()
	c.waiting <- struct{}{}
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			kept = append(kept, w)
			continue
		}
		w.ch <- c.now
	}
	c.waiters = kept
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// waitBlocked waits until the daemon calls After.
func (c *fakeClock) waitBlocked(t *testing.T) {
	t.Helper()
	select {
	case <-c.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon never waited on the clock")
	}
}

// fakeRunner records each run's start time on the fake clock.
type fakeRunner struct {
	clock   *fakeClock
	mu      sync.Mutex
	calls   []time.Time
	active  int
	maxAct  int
	result  Result
	err     error
	during  func()        // runs inside Run, e.g. to make a run long
	started chan struct{} // signalled when Run starts, when set
	release chan struct{} // Run waits for it, when set
}

func (r *fakeRunner) Run(ctx context.Context, s Schedule) (*Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, r.clock.Now())
	r.active++
	r.maxAct = max(r.maxAct, r.active)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()
	if r.started != nil {
		r.started <- struct{}{}
	}
	if r.release != nil {
		<-r.release
	}
	if r.during != nil {
		r.during()
	}
	res := r.result
	return &res, r.err
}

func (r *fakeRunner) callTimes() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// fakeNotifier records alerts.
type fakeNotifier struct {
	mu   sync.Mutex
	sent []alerts.Payload
}

func (n *fakeNotifier) Send(_ context.Context, cfgs []alerts.Config, p alerts.Payload) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, c := range cfgs {
		if c.Wants(p.Event) {
			n.sent = append(n.sent, p)
			break
		}
	}
	return nil
}

func (n *fakeNotifier) events() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, p := range n.sent {
		out = append(out, p.Event)
	}
	return out
}

var start = time.Date(2026, 11, 15, 10, 7, 30, 0, time.UTC)

// newTestDaemon builds a daemon over one schedule on a live experiment with
// a spend cap of capUSD.
func newTestDaemon(t *testing.T, s Schedule, capUSD float64, r *fakeRunner, n *fakeNotifier) (*Daemon, string) {
	t.Helper()
	state := filepath.Join(t.TempDir(), ".schedules")
	load := func(string) (*schemas.Experiment, error) {
		return &schemas.Experiment{Budget: schemas.BudgetSpec{TotalUSD: capUSD}}, nil
	}
	f := &File{Schedules: []Schedule{s}, Alerts: []alerts.Config{{URLEnv: "HOOK"}}}
	var notifier Notifier
	if n != nil {
		notifier = n
	}
	d, err := New(f, Options{Root: t.TempDir(), StateDir: state, Runner: r, Notifier: notifier, Load: load, Clock: r.clock})
	if err != nil {
		t.Fatal(err)
	}
	return d, state
}

// runDaemon starts d and returns a stop function that waits for it.
func runDaemon(t *testing.T, d *Daemon) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	}
}

func TestDaemonFiresAtCronTimes(t *testing.T) {
	clock := newFakeClock(start)
	r := &fakeRunner{clock: clock, result: Result{ExpID: "e_1", Decision: "improved"}}
	d, state := newTestDaemon(t, Schedule{Name: "half", Cron: "*/30 * * * *", Timezone: "UTC", MaxUSDPerDay: 10}, 1, r, nil)
	stop := runDaemon(t, d)
	clock.waitBlocked(t)
	clock.Advance(10 * time.Minute) // 10:17: nothing due, the daemon keeps waiting
	if n := len(r.callTimes()); n != 0 {
		t.Fatalf("ran %d times before 10:30", n)
	}
	clock.Advance(12*time.Minute + 30*time.Second) // 10:30
	clock.waitBlocked(t)
	clock.Advance(30 * time.Minute) // 11:00
	clock.waitBlocked(t)
	stop()
	want := []time.Time{time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC), time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC)}
	got := r.callTimes()
	if len(got) != len(want) || !got[0].Equal(want[0]) || !got[1].Equal(want[1]) {
		t.Fatalf("runs at %v, want %v", got, want)
	}
	last, err := LastRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	if rec := last["half"]; rec.Status != StatusOK || rec.Result.ExpID != "e_1" || !rec.At.Equal(want[1]) {
		t.Fatalf("last record = %+v", rec)
	}
}

func TestDaemonSkipsTimesMissedDuringALongRun(t *testing.T) {
	clock := newFakeClock(start)
	r := &fakeRunner{clock: clock}
	first := true
	r.during = func() {
		if first {
			first = false
			clock.Advance(5 * time.Minute) // the 10:08 run lasts until 10:13
		}
	}
	d, _ := newTestDaemon(t, Schedule{Name: "minutely", Cron: "* * * * *", Timezone: "UTC", MaxUSDPerDay: 100}, 1, r, nil)
	stop := runDaemon(t, d)
	clock.waitBlocked(t)
	clock.Advance(30 * time.Second) // 10:08
	clock.waitBlocked(t)            // after the long run, waiting for 10:14
	clock.Advance(time.Minute)      // 10:14
	clock.waitBlocked(t)
	stop()
	got := r.callTimes()
	want := []time.Time{time.Date(2026, 11, 15, 10, 8, 0, 0, time.UTC), time.Date(2026, 11, 15, 10, 14, 0, 0, time.UTC)}
	if len(got) != 2 || !got[0].Equal(want[0]) || !got[1].Equal(want[1]) {
		t.Fatalf("runs at %v, want %v (10:09 to 10:13 skipped, not queued)", got, want)
	}
	if r.maxAct != 1 {
		t.Fatalf("max concurrent runs = %d, want 1", r.maxAct)
	}
}

func TestDaemonNeverOverlapsASchedule(t *testing.T) {
	clock := newFakeClock(start)
	r := &fakeRunner{clock: clock, started: make(chan struct{}, 1), release: make(chan struct{})}
	d, _ := newTestDaemon(t, Schedule{Name: "s", Cron: "* * * * *", Timezone: "UTC", MaxUSDPerDay: 100}, 1, r, nil)
	stop := runDaemon(t, d)
	clock.waitBlocked(t)
	clock.Advance(time.Minute)
	<-r.started
	rec, err := d.Trigger(context.Background(), "s") // run-now while the timed run is going
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusSkipped || !strings.Contains(rec.Reason, "still running") {
		t.Fatalf("overlapping trigger = %+v", rec)
	}
	close(r.release)
	clock.waitBlocked(t)
	stop()
	if n := len(r.callTimes()); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}
	if _, err := d.Trigger(context.Background(), "nope"); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("unknown schedule = %v", err)
	}
}

func TestDaemonLockHeldByAnotherProcess(t *testing.T) {
	clock := newFakeClock(start)
	r := &fakeRunner{clock: clock}
	d, state := newTestDaemon(t, Schedule{Name: "s", Cron: "0 * * * *", Timezone: "UTC", MaxUSDPerDay: 10}, 1, r, nil)
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(state, "s.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := tryLock(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Error(err)
		}
	}()
	rec, err := d.Trigger(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusSkipped || len(r.callTimes()) != 0 {
		t.Fatalf("trigger with the lock held elsewhere = %+v", rec)
	}
}

func TestDaemonDailyCap(t *testing.T) {
	clock := newFakeClock(start)
	r := &fakeRunner{clock: clock, result: Result{ExpID: "e_x", CostUSD: 3.5}}
	n := &fakeNotifier{}
	s := Schedule{Name: "capped", Cron: "0 * * * *", Timezone: "UTC", MaxUSDPerDay: 10}
	d, state := newTestDaemon(t, s, 4, r, n)
	trigger := func() Record {
		t.Helper()
		rec, err := d.Trigger(context.Background(), "capped")
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	// Spend cap 4 per run, 10 per day: 0+4 and 3.5+4 fit, 7+4 does not.
	if rec := trigger(); rec.Status != StatusOK || rec.SpentTodayUSD != 3.5 {
		t.Fatalf("run 1 = %+v", rec)
	}
	if rec := trigger(); rec.Status != StatusOK || rec.SpentTodayUSD != 7 {
		t.Fatalf("run 2 = %+v", rec)
	}
	rec := trigger()
	if rec.Status != StatusRefused || !strings.Contains(rec.Reason, "daily cap") || rec.SpentTodayUSD != 7 {
		t.Fatalf("run 3 = %+v, want refused by the daily cap", rec)
	}
	if got := n.events(); len(got) != 1 || got[0] != alerts.EventScheduleFailed {
		t.Fatalf("alerts = %v, want one schedule_failed", got)
	}
	if !strings.Contains(n.sent[0].Message, "daily cap") || n.sent[0].Summary.MaxUSDPerDay != 10 {
		t.Fatalf("cap alert = %+v", n.sent[0])
	}
	if len(r.callTimes()) != 2 {
		t.Fatalf("runner called %d times, want 2", len(r.callTimes()))
	}
	// The ledger rolls over at midnight in the schedule's timezone.
	clock.Set(time.Date(2026, 11, 16, 0, 0, 30, 0, time.UTC))
	if rec := trigger(); rec.Status != StatusOK || rec.SpentTodayUSD != 3.5 {
		t.Fatalf("next day = %+v", rec)
	}
	today, err := SpentToday(state, s, clock.Now())
	if err != nil || today != 3.5 {
		t.Fatalf("SpentToday = %v, %v", today, err)
	}
	yesterday, err := SpentToday(state, s, start)
	if err != nil || yesterday != 7 {
		t.Fatalf("yesterday = %v, %v", yesterday, err)
	}
}

func TestDaemonCapByTimezone(t *testing.T) {
	// 20:00 UTC on the 15th is already the 16th in Kolkata (UTC+5:30).
	clock := newFakeClock(time.Date(2026, 11, 15, 17, 0, 0, 0, time.UTC))
	r := &fakeRunner{clock: clock, result: Result{CostUSD: 5}}
	s := Schedule{Name: "ist", Cron: "0 * * * *", Timezone: "Asia/Kolkata", MaxUSDPerDay: 6}
	d, _ := newTestDaemon(t, s, 5, r, nil)
	if rec, _ := d.Trigger(context.Background(), "ist"); rec.Status != StatusOK {
		t.Fatalf("first = %+v", rec)
	}
	clock.Set(time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)) // 23:30 IST, same day
	if rec, _ := d.Trigger(context.Background(), "ist"); rec.Status != StatusRefused {
		t.Fatalf("same IST day = %+v, want refused", rec)
	}
	clock.Set(time.Date(2026, 11, 15, 20, 0, 0, 0, time.UTC)) // 01:30 IST on the 16th
	if rec, _ := d.Trigger(context.Background(), "ist"); rec.Status != StatusOK {
		t.Fatalf("next IST day = %+v, want ok", rec)
	}
}

func TestDaemonRefusesUncappedLiveExperiment(t *testing.T) {
	clock := newFakeClock(start)
	r := &fakeRunner{clock: clock}
	d, _ := newTestDaemon(t, Schedule{Name: "s", Cron: "0 * * * *", Timezone: "UTC", MaxUSDPerDay: 10}, 0, r, nil)
	rec, err := d.Trigger(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusRefused || !strings.Contains(rec.Reason, "no spend cap") || len(r.callTimes()) != 0 {
		t.Fatalf("uncapped = %+v", rec)
	}
}

func TestDaemonAlertsOnOutcome(t *testing.T) {
	tests := []struct {
		name       string
		result     Result
		err        error
		wantStatus string
		wantEvents []string
	}{
		{"improved", Result{ExpID: "e_a", Decision: "improved", Policy: "allow"}, nil, StatusOK, nil},
		{"regressed and blocked", Result{ExpID: "e_b", Decision: "regressed", Action: "block", Policy: "block", RunErrors: 2, Runs: 8, CostUSD: 1.25}, nil,
			StatusFailed, []string{alerts.EventVerdictRegressed, alerts.EventPolicyBlock, alerts.EventRunErrors}},
		{"invalid", Result{ExpID: "e_c", Decision: "invalid"}, nil, StatusFailed, []string{alerts.EventExperimentInvalid}},
		{"inconclusive passes by default", Result{ExpID: "e_d", Decision: "inconclusive"}, nil, StatusOK, nil},
		{"runner error", Result{ExpID: "e_e", CostUSD: 0.5}, errors.New("plan: gateway down"), StatusError, []string{alerts.EventScheduleFailed}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newFakeClock(start)
			r := &fakeRunner{clock: clock, result: tt.result, err: tt.err}
			n := &fakeNotifier{}
			d, _ := newTestDaemon(t, Schedule{Name: "s", Cron: "0 * * * *", Timezone: "UTC", MaxUSDPerDay: 10}, 2, r, n)
			rec, err := d.Trigger(context.Background(), "s")
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s (%+v)", rec.Status, tt.wantStatus, rec)
			}
			if got := n.events(); !slices.Equal(got, tt.wantEvents) {
				t.Fatalf("events = %v, want %v", got, tt.wantEvents)
			}
			if rec.SpentTodayUSD != tt.result.CostUSD {
				t.Fatalf("spent today = %v, want %v", rec.SpentTodayUSD, tt.result.CostUSD)
			}
			for _, p := range n.sent {
				if p.Experiment != tt.result.ExpID || p.Link != alerts.LinkPath(tt.result.ExpID) || p.Schedule != "s" {
					t.Fatalf("payload = %+v", p)
				}
			}
		})
	}
}

func TestEventsFor(t *testing.T) {
	if got := EventsFor(nil, nil); len(got) != 0 {
		t.Fatalf("nil result = %v", got)
	}
	got := EventsFor(&Result{Decision: "invalid", RunErrors: 1}, errors.New("x"))
	want := []string{alerts.EventExperimentInvalid, alerts.EventRunErrors, alerts.EventScheduleFailed}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	load := func(string) (*schemas.Experiment, error) { return &schemas.Experiment{}, nil }
	r := &fakeRunner{clock: newFakeClock(start)}
	if _, err := New(&File{}, Options{StateDir: "x", Load: load}); err == nil {
		t.Fatal("no runner accepted")
	}
	if _, err := New(&File{}, Options{Runner: r, Load: load}); err == nil {
		t.Fatal("no state folder accepted")
	}
	bad := &File{Schedules: []Schedule{{Name: "s", Cron: "bad"}}}
	if _, err := New(bad, Options{Runner: r, Load: load, StateDir: "x"}); err == nil {
		t.Fatal("bad cron accepted")
	}
}

func TestPrune(t *testing.T) {
	l := spendLedger{"a": {"2026-01-01": 1, "2026-11-14": 2}, "b": {"2025-12-31": 3}}
	prune(l, start)
	if _, ok := l["b"]; ok || len(l["a"]) != 1 || l["a"]["2026-11-14"] != 2 {
		t.Fatalf("pruned = %v", l)
	}
}
