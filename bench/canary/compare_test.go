package canary

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

// obs builds runs of one arm: one per task with the given success and
// latency.
func obs(arm string, success []float64, latency []float64) []RunObs {
	var out []RunObs
	for i := range success {
		s := success[i]
		r := RunObs{Task: fmt.Sprintf("t%02d", i+1), Arm: arm, Condition: "clean", Trial: 1, Success: &s}
		if latency != nil {
			l := latency[i]
			r.LatencyMS = &l
		}
		out = append(out, r)
	}
	return out
}

func noFloor(string) float64 { return 0 }

func testByKey(c Comparison, key string) MetricTest {
	for _, t := range c.Tests {
		if t.Key == key {
			return t
		}
	}
	return MetricTest{}
}

func TestCompareExactSignFlip(t *testing.T) {
	ones := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	zeros := []float64{0, 0, 0, 0, 0, 0, 0, 0}
	labels := map[string]string{"A": "a"}
	// All 8 tasks lost success: p = 2 / 2^8.
	c := Compare(obs("A", ones, nil), obs("A", zeros, nil), labels, []string{MetricSuccess}, 0.05, noFloor)
	st := testByKey(c, "A/clean/verified_success")
	if st.Status != TestWorse || math.Abs(*st.P-2.0/256) > 1e-12 || st.Worse != 8 || *st.Delta != -1 {
		t.Fatalf("all lost = %+v", st)
	}
	if !slices.Equal(c.Flagged, []string{"A/clean/verified_success"}) || len(c.Flips) != 8 {
		t.Errorf("flagged %v, flips %d", c.Flagged, len(c.Flips))
	}
	// The same change upwards is better, never flagged.
	c = Compare(obs("A", zeros, nil), obs("A", ones, nil), labels, []string{MetricSuccess}, 0.05, noFloor)
	if st := testByKey(c, "A/clean/verified_success"); st.Status != TestBetter || len(c.Flagged) != 0 {
		t.Fatalf("all gained = %+v", st)
	}
	// Five changed tasks can never reach 0.05 (min p 0.0625): cannot tell.
	five := []float64{0, 0, 0, 0, 0, 1, 1, 1}
	c = Compare(obs("A", ones, nil), obs("A", five, nil), labels, []string{MetricSuccess}, 0.05, noFloor)
	if st := testByKey(c, "A/clean/verified_success"); st.Status != TestCannotTell {
		t.Fatalf("five lost = %+v", st)
	}
	// Nothing changed.
	c = Compare(obs("A", ones, nil), obs("A", ones, nil), labels, []string{MetricSuccess}, 0.05, noFloor)
	if st := testByKey(c, "A/clean/verified_success"); st.Status != TestNoChange {
		t.Fatalf("unchanged = %+v", st)
	}
}

func TestCompareDirectionAndNoiseFloor(t *testing.T) {
	ones := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	base := []float64{1000, 1100, 900, 1000, 1200, 800, 1000, 1000}
	up10 := make([]float64, 8)
	up60 := make([]float64, 8)
	down := make([]float64, 8)
	for i, v := range base {
		up10[i], up60[i], down[i] = v*1.10, v*1.60, v*0.5
	}
	labels := map[string]string{"A": "a"}
	d := &Definition{}
	// Latency up 10% on every task: significant, but under the 25% floor.
	c := Compare(obs("A", ones, base), obs("A", ones, up10), labels, Metrics, 0.10, d.Floor)
	if st := testByKey(c, "A/clean/latency_ms"); st.Status != TestBelowFloor || len(c.Flagged) != 0 {
		t.Fatalf("latency +10%% = %+v", st)
	}
	// Up 60%: worse (higher latency is bad).
	c = Compare(obs("A", ones, base), obs("A", ones, up60), labels, Metrics, 0.10, d.Floor)
	if st := testByKey(c, "A/clean/latency_ms"); st.Status != TestWorse || math.Abs(*st.Relative-0.6) > 1e-9 {
		t.Fatalf("latency +60%% = %+v", st)
	}
	// Down 50%: better, not flagged.
	c = Compare(obs("A", ones, base), obs("A", ones, down), labels, Metrics, 0.10, d.Floor)
	if st := testByKey(c, "A/clean/latency_ms"); st.Status != TestBetter || len(c.Flagged) != 0 {
		t.Fatalf("latency -50%% = %+v", st)
	}
}

func TestHolmWithinAFamily(t *testing.T) {
	// Two targets both lose 7 of 8 tasks: p = 2/128 = 0.0156 each. At
	// family level 0.05 Holm holds the smaller p to 0.025 and the next to
	// 0.05: both rejected. At 0.02 neither is.
	seven := []float64{0, 0, 0, 0, 0, 0, 0, 1}
	ones := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	base := append(obs("A", ones, nil), obs("B", ones, nil)...)
	cur := append(obs("A", seven, nil), obs("B", seven, nil)...)
	labels := map[string]string{"A": "a", "B": "b"}
	c := Compare(base, cur, labels, []string{MetricSuccess}, 0.05, noFloor)
	if len(c.Flagged) != 2 || c.Family != 2 {
		t.Fatalf("level 0.05: flagged %v family %d", c.Flagged, c.Family)
	}
	levels := []float64{testByKey(c, "A/clean/verified_success").Level, testByKey(c, "B/clean/verified_success").Level}
	slices.Sort(levels)
	if levels[0] != 0.025 || levels[1] != 0.05 {
		t.Errorf("Holm levels = %v", levels)
	}
	c = Compare(base, cur, labels, []string{MetricSuccess}, 0.02, noFloor)
	if len(c.Flagged) != 0 {
		t.Fatalf("level 0.02: flagged %v", c.Flagged)
	}
}

func TestNextStateNeedsTwoConsecutiveFlagsOnTheSameKey(t *testing.T) {
	k := []string{"A/clean/verified_success"}
	other := []string{"A/clean/latency_ms"}
	cases := []struct {
		prev, outcome  string
		prevFlag, flag []string
		want           string
	}{
		{"", OutcomeFlagged, nil, k, StateWatching},
		{StateSteady, OutcomeFlagged, nil, k, StateWatching},
		{StateWatching, OutcomeFlagged, k, k, StateDrifted},
		{StateWatching, OutcomeFlagged, other, k, StateWatching},
		{StateDrifted, OutcomeFlagged, k, k, StateDrifted},
		{StateWatching, OutcomeClear, k, nil, StateSteady},
		{StateWatching, OutcomeInvalid, k, nil, StateInvalid},
		{StateSteady, OutcomeStale, nil, nil, StateBaselineStale},
	}
	for _, c := range cases {
		if got := NextState(c.prev, c.prevFlag, c.outcome, c.flag); got != c.want {
			t.Errorf("NextState(%q, %v, %q, %v) = %q, want %q", c.prev, c.prevFlag, c.outcome, c.flag, got, c.want)
		}
	}
}

// TestObserveServedModelChangeFromRows reads fixture gateway rows: the
// same alias answered by another dated model, a system fingerprint, a
// format error, and the first prompt's token count.
func TestObserveServedModelChangeFromRows(t *testing.T) {
	ts := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	row := func(id string, at int, served string, fp string, tokens int64, lat float64) schemas.GatewayRequest {
		raw, _ := json.Marshal(map[string]any{"response": map[string]any{"system_fingerprint": fp}})
		return schemas.GatewayRequest{ID: id, Timestamp: ts.Add(time.Duration(at) * time.Second), Status: "success", Kind: "message", Provider: "openai",
			ModelRequested: "gpt-5", ModelServed: served, LatencyMS: lat, Tokens: &schemas.Tokens{Fresh: tokens, Out: 10}, Raw: raw}
	}
	succ := schemas.Observed(1.0, schemas.EvidenceOutcome)
	sr := func(rows ...schemas.GatewayRequest) (schemas.SealedRun, *schemas.RequestLog) {
		return schemas.SealedRun{Slot: schemas.Slot{Task: "t01", Arm: "A", Condition: "clean", Trial: 1, Counted: true},
			Run:     schemas.Run{RunID: "r1", Outcome: schemas.OutcomePass},
			Metrics: map[string]schemas.Value{MetricSuccess: succ},
			Gates:   []schemas.GateResult{{Gate: "model_fidelity", Fired: true, Effect: schemas.GateInvalidate}}}, &schemas.RequestLog{Requests: rows}
	}
	bad := schemas.GatewayRequest{ID: "q9", Timestamp: ts.Add(5 * time.Second), Status: "error", Kind: "message", ModelRequested: "gpt-5", ErrorStatus: 400}
	b, bl := sr(row("q2", 2, "gpt-5-2026-08-01", "fp_a", 999, 900), row("q1", 1, "gpt-5-2026-08-01", "fp_a", 4000, 1100))
	c, cl := sr(row("q1", 1, "gpt-5-2026-11-01", "fp_b", 4210, 2100), bad)
	before, now := Observe(b, bl, nil), Observe(c, cl, nil)
	if before.FirstPromptTokens == nil || *before.FirstPromptTokens != 4000 || *before.LatencyMS != 1000 {
		t.Fatalf("baseline obs = %+v", before)
	}
	if len(before.InvalidGates) != 0 {
		t.Errorf("model fidelity counted as a harness fault: %v", before.InvalidGates)
	}
	if now.FormatErrors != 1 || now.ModelErrors != 1 || !slices.Equal(now.Fingerprints, []string{"fp_b"}) {
		t.Fatalf("current obs = %+v", now)
	}
	bx := &Execution{ExpID: "e1", Arms: map[string]string{"A": "openai/gpt-5"}, Runs: []RunObs{before}}
	cx := &Execution{ExpID: "e2", Arms: map[string]string{"A": "openai/gpt-5"}, Runs: []RunObs{now}, Clusters: []ClusterRef{}}
	ch := WhatChanged(bx, cx, nil, nil)
	a := ch.Arms[0]
	if !a.ServedChanged || !a.FingerprintChanged || a.ProviderChanged || a.PromptChanged != 1 {
		t.Fatalf("changes = %+v", a)
	}
	moved := strings.Join(ch.Moved, "\n")
	for _, want := range []string{"gpt-5-2026-08-01 before, gpt-5-2026-11-01 now", "system fingerprint changed", "identical first prompt changed on 1 of 1"} {
		if !strings.Contains(moved, want) {
			t.Errorf("moved lacks %q:\n%s", want, moved)
		}
	}
	base := &Baseline{ExpID: "e1", Served: ServedSummary(bx)}
	if got := ServedChangedSince(base, ServedSummary(cx)); len(got) != 1 {
		t.Errorf("served changed since baseline = %v", got)
	}
}

func TestProviderEquivalence(t *testing.T) {
	d := &Definition{Targets: []Target{{Provider: "anthropic", Model: "claude-sonnet-4-5"}, {Provider: "bedrock", Model: "anthropic/claude-sonnet-4-5"}}}
	if p := EquivalencePairs(d); len(p) != 1 {
		t.Fatalf("pairs = %v", p)
	}
	ones := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	lat := []float64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000}
	slow := []float64{2000, 2100, 1900, 2000, 2200, 1800, 2000, 2000}
	x := &Execution{Runs: append(obs("A", ones, lat), obs("B", ones, slow)...)}
	eq := CompareProviders(d, x, 0.10)
	if len(eq) != 1 || eq[0].Verdict != EquivDiffers || !strings.Contains(eq[0].Reason, "latency_ms") {
		t.Fatalf("slow provider = %+v", eq)
	}
	// Two tasks only: no test can reach the level.
	x = &Execution{Runs: append(obs("A", []float64{1, 0}, nil), obs("B", []float64{0, 1}, nil)...)}
	if eq = CompareProviders(d, x, 0.10); eq[0].Verdict != EquivCannotTell {
		t.Fatalf("two tasks = %+v", eq[0])
	}
	// Identical on 8 tasks: nothing detected, which is not proof.
	x = &Execution{Runs: append(obs("A", ones, lat), obs("B", ones, lat)...)}
	if eq = CompareProviders(d, x, 0.10); eq[0].Verdict != EquivNoDifference || !strings.Contains(eq[0].Reason, "not proof") {
		t.Fatalf("identical = %+v", eq[0])
	}
}

func TestFingerprintIgnoresRunIdentity(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e1", SuiteID: "s", SuiteVersion: "1", SuiteLockHash: "L", Arms: map[string]schemas.ArmManifest{"A": {ConfigHash: "c1"}},
		Slots: []schemas.Slot{{Task: "t1", Condition: "clean", Trial: 1, Arm: "A", Counted: true}}, Experiment: schemas.Experiment{Seed: 1}}
	m2 := *m
	m2.ExpID, m2.CreatedAt, m2.Experiment.Seed, m2.Experiment.Budget.TotalUSD = "e2", time.Now(), 9, 5
	if Fingerprint(m, nil) != Fingerprint(&m2, nil) {
		t.Error("experiment id, time, seed or budget changed the fingerprint")
	}
	m2.Arms = map[string]schemas.ArmManifest{"A": {ConfigHash: "c2"}}
	if Fingerprint(m, nil) == Fingerprint(&m2, nil) {
		t.Error("a new agent config hash kept the fingerprint")
	}
	inj := *m
	inj.Slots = []schemas.Slot{{Task: "t1", Condition: "model-outage", Trial: 1, Arm: "A", Counted: true}}
	if Fingerprint(&inj, map[string]string{"model-outage": "clean"}) != Fingerprint(m, nil) {
		t.Error("an injected run does not map back to the watched condition")
	}
}

// daemonClock is a fake schedule.Clock that fires waiters when advanced.
type daemonClock struct {
	mu  sync.Mutex
	now time.Time
	ch  chan time.Time
}

func (c *daemonClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *daemonClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.now = c.now.Add(d) // jump straight to the next trigger
	now := c.now
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- now
	return ch
}

// canaryJob adapts the runner to schedule.Runner the way the CLI does.
type canaryJob struct {
	r    *Runner
	d    *Definition
	done chan []Record
}

func (j canaryJob) Run(ctx context.Context, s schedule.Schedule) (*schedule.Result, error) {
	recs, err := j.r.Run(ctx, j.d, RunOptions{})
	res := &schedule.Result{}
	for _, r := range recs {
		res.CostUSD += r.CostUSD
	}
	j.done <- recs
	return res, err
}

// TestScheduleDaemonRunsTheCanaryOnItsCron drives the real schedule daemon
// with a fake clock: a canary entry fires at its cron times and each
// trigger is one canary look.
func TestScheduleDaemonRunsTheCanaryOnItsCron(t *testing.T) {
	clock := &daemonClock{now: time.Date(2026, 11, 18, 5, 0, 0, 0, time.UTC)}
	ex := newFakeExec(clock.Now, good)
	d := testDef()
	d.Schedule = &ScheduleSpec{Cron: "0 6 * * *", Timezone: "UTC"}
	r := &Runner{Root: t.TempDir(), Exec: ex, Now: clock.Now}
	if _, err := r.Baseline(context.Background(), d, BaselineOptions{}); err != nil {
		t.Fatal(err)
	}
	file := &schedule.File{Schedules: []schedule.Schedule{{Name: d.ScheduleName(), Experiment: "canaries/nightly.yaml", Cron: d.Schedule.Cron,
		Timezone: "UTC", MaxUSDPerDay: d.Budget.PerDayUSD}}}
	job := canaryJob{r: r, d: d, done: make(chan []Record, 4)}
	load := func(string) (*schemas.Experiment, error) {
		return &schemas.Experiment{Budget: schemas.BudgetSpec{TotalUSD: d.Budget.PerRunUSD}}, nil
	}
	dm, err := schedule.New(file, schedule.Options{StateDir: t.TempDir(), Runner: job, Load: load, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = dm.Run(ctx) }()
	var at []time.Time
	for i := 0; i < 2; i++ {
		select {
		case recs := <-job.done:
			if len(recs) != 1 || recs[0].State != StateSteady {
				t.Errorf("trigger %d = %v", i, statesOf(recs))
			}
			at = append(at, recs[0].Started)
		case <-time.After(5 * time.Second):
			t.Fatal("the daemon did not trigger the canary")
		}
	}
	cancel()
	if at[0].Hour() != 6 || at[1].Sub(at[0]) != 24*time.Hour {
		t.Errorf("triggers at %v, want 06:00 on consecutive days", at)
	}
}
