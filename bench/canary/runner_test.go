package canary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/schemas"
)

// world is what the fake provider does on each executed experiment.
type world struct {
	success float64 // per-run success on every task
	served  string  // model that answers
	latency float64
	prompt  int64
	fail    bool  // the harness fails: infra errors, nothing scored
	fired   *bool // the run's probe receipt: armed, then fired or not
}

// fakeExec is a canary.Executor over in-memory experiments.
type fakeExec struct {
	fp       string
	tasks    int
	arms     map[string]string
	cost     float64
	forecast *float64
	now      func() time.Time
	worlds   []world // one per Execute call; the last repeats
	execs    map[string]*Execution
	n        int
	planErr  error
	executed int
	planned  int
}

func newFakeExec(now func() time.Time, ws ...world) *fakeExec {
	return &fakeExec{fp: "fp1", tasks: 8, arms: map[string]string{"A": "anthropic/claude-sonnet-4-5"}, now: now, worlds: ws, execs: map[string]*Execution{}}
}

func (f *fakeExec) Plan(_ context.Context, d *Definition, inject string) (*Plan, error) {
	f.planned++
	if f.planErr != nil {
		return nil, f.planErr
	}
	return &Plan{ExpID: fmt.Sprintf("e_plan_%d", f.n+1), Fingerprint: f.fp, Slots: f.tasks * len(f.arms), ForecastUSD: f.forecast, Rename: d.Rename(inject)}, nil
}

func (f *fakeExec) Execute(_ context.Context, p *Plan) (string, float64, error) {
	f.n++
	f.executed++
	w := f.worlds[min(f.n, len(f.worlds))-1]
	id := fmt.Sprintf("e_canary_%d", f.n)
	x := &Execution{ExpID: id, ManifestHash: "h_" + id, Fingerprint: f.fp, CreatedAt: f.now(), Slots: p.Slots, Arms: f.arms, Clusters: []ClusterRef{}}
	for arm := range f.arms {
		aw := w
		if arm == "B" {
			aw.latency *= 1.6 // the second provider of the model answers slower
		}
		for t := 0; t < f.tasks; t++ {
			x.Runs = append(x.Runs, fakeRun(arm, fmt.Sprintf("t%02d", t+1), aw))
		}
	}
	if w.success == 0 && !w.fail {
		x.Clusters = []ClusterRef{{Signature: "c_outage", Title: "model unavailable", Count: f.tasks, Tasks: []string{"t01"}}}
	}
	f.execs[id] = x
	return id, f.cost, nil
}

func (f *fakeExec) Observe(_ context.Context, expID string, _ map[string]string) (*Execution, error) {
	x, ok := f.execs[expID]
	if !ok {
		return nil, fmt.Errorf("%s: %w", expID, schemas.ErrNotFound)
	}
	return x, nil
}

func fakeRun(arm, task string, w world) RunObs {
	r := RunObs{RunID: "r_" + arm + task, Task: task, Arm: arm, Condition: "clean", Trial: 1, Outcome: "pass",
		Requested: []string{"claude-sonnet-4-5"}, Providers: []string{"anthropic"}, ModelRequests: 3, FaultFired: w.fired}
	if w.fail {
		r.Outcome, r.InfraError = "infra_error", true
		return r
	}
	s, cost, tok, lat, prompt := w.success, 0.01, 1000.0, w.latency, w.prompt
	r.Success, r.CostUSD, r.Tokens, r.LatencyMS, r.FirstPromptTokens = &s, &cost, &tok, &lat, &prompt
	r.Served = []string{w.served}
	return r
}

// fakeNotifier records payloads.
type fakeNotifier struct{ sent []alerts.Payload }

func (n *fakeNotifier) Send(_ context.Context, _ []alerts.Config, p alerts.Payload) error {
	n.sent = append(n.sent, p)
	return nil
}

func (n *fakeNotifier) events() []string {
	var out []string
	for _, p := range n.sent {
		out = append(out, p.Event)
	}
	return out
}

func testDef() *Definition {
	return &Definition{Name: "nightly", Agent: "agents/toy.yaml", Suite: "coding-core@1.0.0", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}},
		Trials: 1, Targets: []Target{{Provider: "anthropic", Model: "claude-sonnet-4-5"}}, Budget: Budget{PerRunUSD: 1, PerDayUSD: 3},
		Alerts: []alerts.Config{{URLEnv: "CANARY_HOOK"}}, Schedule: &ScheduleSpec{Cron: "0 6 * * *", Timezone: "UTC"}}
}

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { c.t = c.t.Add(time.Minute); return c.t }

func newTestRunner(t *testing.T, ex *fakeExec, clock *testClock) (*Runner, *fakeNotifier) {
	n := &fakeNotifier{}
	return &Runner{Root: t.TempDir(), Exec: ex, Notifier: n, Now: clock.now}, n
}

var good = world{success: 1, served: "claude-sonnet-4-5-20250929", latency: 1500, prompt: 4000}

func TestBaselineThenSteadyThenConfirmedDrift(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	drift := world{success: 0, served: "claude-sonnet-4-5-20261101", latency: 3200, prompt: 4100}
	ex := newFakeExec(clock.now, good, good, drift)
	r, n := newTestRunner(t, ex, clock)
	d := testDef()
	ctx := context.Background()

	if _, err := r.Run(ctx, d, RunOptions{}); !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("run before baseline: err = %v, want ErrNoBaseline", err)
	}
	b, err := r.Baseline(ctx, d, BaselineOptions{Note: "known good"})
	if err != nil || b.Outcome != OutcomePinned || b.State != StateSteady {
		t.Fatalf("baseline = %+v, %v", b, err)
	}
	recs, err := r.Run(ctx, d, RunOptions{})
	if err != nil || len(recs) != 1 || recs[0].State != StateSteady || recs[0].Outcome != OutcomeClear {
		t.Fatalf("steady run = %+v, %v", recs, err)
	}
	recs, err = r.Run(ctx, d, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].State != StateWatching || recs[1].Kind != KindConfirmation || recs[1].State != StateDrifted {
		t.Fatalf("drift run states = %v", statesOf(recs))
	}
	if !slices.Contains(recs[1].Comparison.Flagged, "A/clean/verified_success") {
		t.Errorf("flagged = %v, want success on A", recs[1].Comparison.Flagged)
	}
	moved := strings.Join(recs[1].Changes.Moved, "\n")
	for _, want := range []string{"served model changed", "claude-sonnet-4-5-20261101", "median model latency", "identical first prompt", "new failure cluster c_outage", "lost success"} {
		if !strings.Contains(moved, want) {
			t.Errorf("what changed lacks %q:\n%s", want, moved)
		}
	}
	ev := n.events()
	if !slices.Contains(ev, alerts.EventCanaryDrifted) || !slices.Contains(ev, alerts.EventCanaryModelChanged) {
		t.Errorf("alerts = %v, want canary_drifted and canary_model_changed", ev)
	}
	// The model change alerts once per baseline, the drift once per entry.
	if count(ev, alerts.EventCanaryModelChanged) != 1 || count(ev, alerts.EventCanaryDrifted) != 1 {
		t.Errorf("alerts = %v, want each once", ev)
	}
	for _, p := range n.sent {
		if p.Event == alerts.EventCanaryDrifted && (p.Canary != "nightly" || len(p.Changes) == 0 || p.Decision != StateDrifted) {
			t.Errorf("drift payload = %+v", p)
		}
	}
	// A further flagged look stays drifted without a second alert.
	if recs, err = r.Run(ctx, d, RunOptions{}); err != nil || recs[len(recs)-1].State != StateDrifted {
		t.Fatalf("still drifted = %v, %v", statesOf(recs), err)
	}
	if count(n.events(), alerts.EventCanaryDrifted) != 1 {
		t.Errorf("drift alerted again: %v", n.events())
	}
	det, err := BuildDetail(r.Root, d, clock.now())
	if err != nil || det.Summary.State != StateDrifted || len(det.History) != 1 || len(det.History[0].Points) < 4 {
		t.Fatalf("detail = %+v, %v", det.Summary, err)
	}
	if det.History[0].Points[0].Regression.Status != "first" || det.History[0].Points[len(det.History[0].Points)-1].Release != "regressed" {
		t.Errorf("history points = %+v", det.History[0].Points)
	}
}

func TestFlagThenClearReturnsToSteady(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	bad := world{success: 0, served: good.served, latency: 1500, prompt: 4000}
	ex := newFakeExec(clock.now, good, bad, good)
	r, n := newTestRunner(t, ex, clock)
	d := testDef()
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	recs, err := r.Run(context.Background(), d, RunOptions{})
	if err != nil || len(recs) != 2 || recs[0].State != StateWatching || recs[1].State != StateSteady {
		t.Fatalf("flag then clear = %v, %v", statesOf(recs), err)
	}
	if slices.Contains(n.events(), alerts.EventCanaryDrifted) {
		t.Errorf("a single flag alerted: %v", n.events())
	}
}

func TestInvalidLookNeverDrifts(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.now, good, world{fail: true})
	r, n := newTestRunner(t, ex, clock)
	d := testDef()
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	recs, err := r.Run(context.Background(), d, RunOptions{})
	if err != nil || len(recs) != 1 || recs[0].State != StateInvalid || recs[0].Comparison != nil {
		t.Fatalf("invalid look = %v, %v", statesOf(recs), err)
	}
	if !strings.Contains(recs[0].Reason, "graded coverage") || !slices.Contains(n.events(), alerts.EventExperimentInvalid) {
		t.Errorf("reason %q, alerts %v", recs[0].Reason, n.events())
	}
}

func TestInvalidBaselineIsNotPinned(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.now, world{fail: true})
	r, _ := newTestRunner(t, ex, clock)
	d := testDef()
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err == nil {
		t.Fatal("an invalid baseline was pinned")
	}
	if b, _ := CurrentBaseline(r.Root, d.Name); b != nil {
		t.Errorf("baseline = %+v, want none", b)
	}
}

func TestChangedConfigIsBaselineStaleAndRunsNothing(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.now, good)
	r, _ := newTestRunner(t, ex, clock)
	d := testDef()
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	ex.fp = "fp2" // the agent config or the suite lock changed
	recs, err := r.Run(context.Background(), d, RunOptions{})
	if err != nil || recs[0].State != StateBaselineStale || recs[0].Outcome != OutcomeStale {
		t.Fatalf("stale = %v, %v", statesOf(recs), err)
	}
	if ex.executed != 1 {
		t.Errorf("executed %d experiments, want only the baseline", ex.executed)
	}
	// Re-pinning from an experiment of another config is refused.
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{From: "e_canary_1"}); err == nil {
		t.Error("pinned an experiment of another fingerprint")
	}
	// A fresh baseline of the new config clears it; the history keeps both.
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	bs, _ := ReadBaselines(r.Root, d.Name)
	if len(bs) != 2 || bs[1].Fingerprint != "fp2" {
		t.Errorf("baselines = %+v", bs)
	}
}

func TestOldBaselineReadsStale(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.now, good)
	r, _ := newTestRunner(t, ex, clock)
	d := testDef()
	d.MaxBaselineAgeDays = 7
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(8 * 24 * time.Hour)
	recs, err := r.Run(context.Background(), d, RunOptions{})
	if err != nil || recs[0].State != StateBaselineStale || recs[0].Outcome != OutcomeClear {
		t.Fatalf("old baseline = %v, %v", statesOf(recs), err)
	}
}

func TestDailyCapRefusesBeforePlanningSpend(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.now, good)
	ex.cost = 1
	r, n := newTestRunner(t, ex, clock)
	d := testDef() // per run 1, per day 3
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if recs, err := r.Run(context.Background(), d, RunOptions{}); err != nil || recs[0].Outcome != OutcomeClear {
			t.Fatalf("run %d = %v, %v", i, statesOf(recs), err)
		}
	}
	plannedBefore := ex.planned
	recs, err := r.Run(context.Background(), d, RunOptions{})
	if err != nil || recs[0].Outcome != OutcomeRefused || !strings.Contains(recs[0].Reason, "daily cap") {
		t.Fatalf("fourth run of the day = %+v, %v", recs, err)
	}
	if ex.planned != plannedBefore {
		t.Errorf("the daily cap refusal planned first (%d plans, want %d)", ex.planned, plannedBefore)
	}
	if ex.executed != 3 {
		t.Errorf("executed %d, want 3", ex.executed)
	}
	if !slices.Contains(n.events(), alerts.EventScheduleFailed) {
		t.Errorf("alerts = %v", n.events())
	}
	// The next day the cap resets.
	clock.t = clock.t.Add(24 * time.Hour)
	if recs, err := r.Run(context.Background(), d, RunOptions{}); err != nil || recs[0].Outcome != OutcomeClear {
		t.Fatalf("next day = %v, %v", statesOf(recs), err)
	}
}

func TestForecastAbovePerRunCapRefuses(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.now, good)
	high := 2.5
	ex.forecast = &high
	r, _ := newTestRunner(t, ex, clock)
	rec, err := r.Baseline(context.Background(), testDef(), BaselineOptions{})
	if err == nil || rec.Outcome != OutcomeRefused || !strings.Contains(rec.Reason, "forecast") || ex.executed != 0 {
		t.Fatalf("baseline = %+v, %v, executed %d", rec, err, ex.executed)
	}
}

func TestConfirmationDeferredUnderTheCap(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.now, good, world{success: 0, served: good.served, latency: 1500, prompt: 4000})
	ex.cost = 1
	r, _ := newTestRunner(t, ex, clock)
	d := testDef()
	d.Budget.PerDayUSD = 2
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	recs, err := r.Run(context.Background(), d, RunOptions{})
	if err != nil || len(recs) != 2 || recs[1].Outcome != OutcomeRefused || recs[1].State != StateWatching ||
		!strings.Contains(recs[1].Reason, "confirmation deferred") {
		t.Fatalf("deferred confirmation = %+v, %v", recs, err)
	}
	// Next day the scheduled look confirms the drift.
	clock.t = clock.t.Add(24 * time.Hour)
	recs, err = r.Run(context.Background(), d, RunOptions{})
	if err != nil || recs[0].State != StateDrifted {
		t.Fatalf("next look = %v, %v", statesOf(recs), err)
	}
}

func TestCapsRequiredUnlessSmoke(t *testing.T) {
	d := testDef()
	d.Budget = Budget{}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "per_run_usd") {
		t.Fatalf("validate without caps = %v", err)
	}
	d.Smoke = true
	if err := d.Validate(); err != nil {
		t.Fatalf("smoke without caps = %v", err)
	}
	d.Smoke = false
	d.Budget = Budget{PerRunUSD: 5, PerDayUSD: 2}
	if err := d.Validate(); err == nil {
		t.Fatal("per-run cap above the daily cap passed")
	}
}

func TestInjectOnlyOnSmoke(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	r, _ := newTestRunner(t, newFakeExec(clock.now, good), clock)
	if _, err := r.Run(context.Background(), testDef(), RunOptions{Inject: "model-outage"}); err == nil || !strings.Contains(err.Error(), "smoke") {
		t.Fatalf("inject on a live canary = %v", err)
	}
	d := testDef()
	d.Smoke = true
	e := d.Experiment([]string{"t01"}, "model-outage")
	if len(e.Conditions) != 1 || e.Conditions[0].ID != "model-outage" || e.Primary.Condition != "model-outage" || d.Rename("model-outage")["model-outage"] != "clean" {
		t.Errorf("injected experiment = %+v", e.Conditions)
	}
}

func TestBusyCanaryRefusesASecondRun(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	r, _ := newTestRunner(t, newFakeExec(clock.now, good), clock)
	unlock, err := r.lock("nightly")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := r.Baseline(context.Background(), testDef(), BaselineOptions{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second run = %v, want ErrBusy", err)
	}
}

func TestLoadIsStrictAndNameMatchesFile(t *testing.T) {
	dir := t.TempDir()
	d := testDef()
	if err := Save(filepath.Join(dir, "nightly.yaml"), d); err != nil {
		t.Fatal(err)
	}
	got, err := Load(filepath.Join(dir, "nightly.yaml"))
	if err != nil || got.Name != "nightly" || got.Budget.PerDayUSD != 3 || got.Tasks.IDs[1] != "t02" {
		t.Fatalf("load = %+v, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "typo.yaml"), []byte("name: typo\nper_day: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, "typo.yaml")); err == nil {
		t.Error("an unknown key loaded")
	}
	if err := Save(filepath.Join(dir, "other.yaml"), d); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, "other.yaml")); err == nil {
		t.Error("a name that differs from the file loaded")
	}
	defs, errs := LoadAll(dir)
	if len(defs) != 1 || len(errs) != 2 {
		t.Errorf("load all = %d defs, errs %v", len(defs), errs)
	}
}

func statesOf(recs []Record) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.Kind+":"+r.Outcome+":"+r.State)
	}
	return out
}

func count(xs []string, x string) int {
	n := 0
	for _, v := range xs {
		if v == x {
			n++
		}
	}
	return n
}

// TestWriteUIFixtures writes the UI and handler fixtures from a scenario
// when CANARY_FIXTURE_OUT names a folder: a drifted canary watching one
// model through two providers, and a steady smoke canary.
func TestWriteUIFixtures(t *testing.T) {
	out := os.Getenv("CANARY_FIXTURE_OUT")
	if out == "" {
		t.Skip("set CANARY_FIXTURE_OUT to write ui/public/fixtures/canaries*.json")
	}
	clock := &testClock{t: time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	drift := world{success: 0, served: "claude-sonnet-4-5-20261101", latency: 3200, prompt: 4100}
	ex := newFakeExec(clock.now, good, good, good, drift)
	ex.arms = map[string]string{"A": "anthropic/claude-sonnet-4-5", "B": "bedrock/claude-sonnet-4-5"}
	ex.cost = 0.42
	r := &Runner{Root: root, Exec: ex, Now: clock.now}
	d := testDef()
	d.Description = "Nightly 8-task check of the support agent on Sonnet through two providers"
	d.Targets = append(d.Targets, Target{Provider: "bedrock", Model: "claude-sonnet-4-5"})
	d.Budget = Budget{PerRunUSD: 1, PerDayUSD: 4}
	ctx := context.Background()
	if _, err := r.Baseline(ctx, d, BaselineOptions{Note: "pinned after the 1.4 release"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		clock.t = clock.t.Add(24 * time.Hour)
		if _, err := r.Run(ctx, d, RunOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	sclock := &testClock{t: time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC)}
	sx := newFakeExec(sclock.now, good)
	sx.tasks = 2
	sr := &Runner{Root: root, Exec: sx, Now: sclock.now}
	sd := testDef()
	sd.Name, sd.Smoke, sd.Budget, sd.Alerts, sd.Schedule = "smoke-toy", true, Budget{}, nil, nil
	sd.Description = "Scripted toy agent against the fault proxy's scripted model: zero cost, never a finding"
	if _, err := sr.Baseline(ctx, sd, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := sr.Run(ctx, sd, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	now := clock.now()
	det, err := BuildDetail(root, d, now)
	if err != nil {
		t.Fatal(err)
	}
	sdet, err := BuildDetail(root, sd, now)
	if err != nil {
		t.Fatal(err)
	}
	list, err := BuildList(root, []*Definition{d, sd}, map[string]string{"broken": "canary broken: budget.per_run_usd and budget.per_day_usd are required for a canary that can spend (only a smoke canary may leave them out)"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{"canaries.json": list, "canaries.nightly.json": det, "canaries.smoke-toy.json": sdet} {
		b, _ := json.MarshalIndent(v, "", "  ")
		if err := os.WriteFile(filepath.Join(out, name), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFailedRepinKeepsTheConfirmationPair: a baseline attempt that pinned
// nothing (refused here) sits between two flags and does not break them.
func TestFailedRepinKeepsTheConfirmationPair(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
	bad := world{success: 0, served: good.served, latency: 1500, prompt: 4000}
	ex := newFakeExec(clock.now, good, bad)
	r, _ := newTestRunner(t, ex, clock)
	d := testDef()
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	if recs, err := r.Run(context.Background(), d, RunOptions{NoConfirm: true}); err != nil || recs[0].State != StateWatching {
		t.Fatalf("first flag = %v, %v", statesOf(recs), err)
	}
	high := 9.0
	ex.forecast = &high
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err == nil {
		t.Fatal("re-pin over the forecast was not refused")
	}
	ex.forecast = nil
	if recs, err := r.Run(context.Background(), d, RunOptions{}); err != nil || recs[0].State != StateDrifted {
		t.Fatalf("second flag = %v, %v", statesOf(recs), err)
	}
}

// TestInjectedDriftConfirmsAndAFaultThatMissedIsInvalid: on a smoke canary
// a forced outage that fired drifts after its confirmation; one whose fault
// never reached the runs (a shared gateway rerouted them) is invalid.
func TestInjectedDriftConfirmsAndAFaultThatMissedIsInvalid(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		fired *bool
		want  string
	}{{&yes, StateDrifted}, {&no, StateInvalid}} {
		clock := &testClock{t: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)}
		ex := newFakeExec(clock.now, good, world{success: 0, served: good.served, latency: 1500, prompt: 4000, fired: c.fired})
		r, _ := newTestRunner(t, ex, clock)
		d := testDef()
		d.Smoke, d.Budget = true, Budget{}
		if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
			t.Fatal(err)
		}
		recs, err := r.Run(context.Background(), d, RunOptions{Inject: "model-outage"})
		if err != nil || recs[len(recs)-1].State != c.want || recs[0].Injected != "model-outage" {
			t.Errorf("fired %v: %v, %v", *c.fired, statesOf(recs), err)
		}
	}
}
