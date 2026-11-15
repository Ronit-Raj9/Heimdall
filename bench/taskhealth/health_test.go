package taskhealth

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeSource is an in-memory record: manifests, counted sealed runs per
// grade source, every attempt per slot, and regrades.
type fakeSource struct {
	manifests map[string]*schemas.Manifest
	sealed    map[string]map[string][]schemas.SealedRun // exp -> grade source -> runs
	attempts  map[string][]schemas.RunRecord            // slot id -> runs
	regrades  map[string][]Regrade
}

func newFakeSource() *fakeSource {
	return &fakeSource{manifests: map[string]*schemas.Manifest{}, sealed: map[string]map[string][]schemas.SealedRun{},
		attempts: map[string][]schemas.RunRecord{}, regrades: map[string][]Regrade{}}
}

func (f *fakeSource) Experiments(context.Context) ([]string, error) {
	var ids []string
	for id := range f.manifests {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

func (f *fakeSource) Manifest(_ context.Context, id string) (*schemas.Manifest, error) {
	if m, ok := f.manifests[id]; ok {
		return m, nil
	}
	return nil, schemas.ErrNotFound
}

func (f *fakeSource) SealedRuns(_ context.Context, id, gs string) ([]schemas.SealedRun, error) {
	return f.sealed[id][gs], nil
}

func (f *fakeSource) SlotAttempts(_ context.Context, _, slot string) ([]schemas.RunRecord, error) {
	return f.attempts[slot], nil
}

func (f *fakeSource) Regrades(_ context.Context, id string) ([]Regrade, error) {
	return f.regrades[id], nil
}

// expBuilder writes one experiment into a fakeSource.
type expBuilder struct {
	f *fakeSource
	m *schemas.Manifest
	n int
}

func (f *fakeSource) exp(id string, arms map[string]schemas.AgentConfig) *expBuilder {
	m := &schemas.Manifest{ExpID: id, SuiteID: "s", Arms: map[string]schemas.ArmManifest{}}
	for arm, cfg := range arms {
		m.Arms[arm] = schemas.ArmManifest{Config: cfg, ConfigHash: "h-" + cfg.ID}
	}
	f.manifests[id] = m
	f.sealed[id] = map[string][]schemas.SealedRun{}
	return &expBuilder{f: f, m: m}
}

// run adds one counted sealed run (and its attempt); extra attempts before
// it are aborted_infra retries.
func (b *expBuilder) run(task, arm string, outcome schemas.Outcome, g *schemas.Grade, abortedBefore int) string {
	b.n++
	slotID := fmt.Sprintf("%s-slot%d", b.m.ExpID, b.n)
	runID := fmt.Sprintf("%s-run%d", b.m.ExpID, b.n)
	slot := schemas.Slot{SlotID: slotID, ExpID: b.m.ExpID, Suite: "s", Task: task, TaskVersion: "1.0.0", Arm: arm, Condition: "clean", Trial: b.n, Counted: true}
	b.m.Slots = append(b.m.Slots, slot)
	cfg := b.m.Arms[arm].Config
	run := schemas.Run{RunID: runID, SlotID: slotID, ExpID: b.m.ExpID, Outcome: outcome,
		Agent: schemas.RunAgentInfo{ConfigID: cfg.ID, Adapter: cfg.Adapter, ConfigHash: "h-" + cfg.ID}}
	for i := 0; i < abortedBefore; i++ {
		ab := run
		ab.RunID = fmt.Sprintf("%s-ab%d", runID, i)
		ab.Outcome = schemas.OutcomeAbortedInfra
		b.f.attempts[slotID] = append(b.f.attempts[slotID], schemas.RunRecord{Run: ab, Sealed: true})
	}
	b.f.attempts[slotID] = append(b.f.attempts[slotID], schemas.RunRecord{Run: run, Sealed: true})
	b.f.sealed[b.m.ExpID][""] = append(b.f.sealed[b.m.ExpID][""], schemas.SealedRun{Slot: slot, Run: run, Grade: g, GradeSource: "original"})
	return runID
}

func grade(passed bool, diff string) *schemas.Grade {
	return &schemas.Grade{Passed: passed, DiffSHA256: diff, SuiteVersion: "1.0.0", GraderVersion: "g1"}
}

func outcomeOf(passed bool) schemas.Outcome {
	if passed {
		return schemas.OutcomePass
	}
	return schemas.OutcomeFail
}

var (
	agentA = schemas.AgentConfig{ID: "alpha", Adapter: "command"}
	agentB = schemas.AgentConfig{ID: "beta", Adapter: "command"}
	toy    = schemas.AgentConfig{ID: "toy", Adapter: "scripted", Scripted: &schemas.ScriptedSpec{Kind: "toy"}}
	orac   = schemas.AgentConfig{ID: "oracle", Adapter: "scripted", Scripted: &schemas.ScriptedSpec{Kind: "oracle"}}
)

func testSuite() *schemas.Suite {
	s := &schemas.Suite{ID: "s", Version: "1.0.0", Lock: &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{}}}
	for _, id := range []string{"easy", "hard", "mixed", "flaky", "dead", "infra"} {
		s.Tasks = append(s.Tasks, &schemas.Task{ID: id, Version: "1.0.0"})
		s.TaskIDs = append(s.TaskIDs, id)
	}
	s.TaskByID("easy").Difficulty = "hard" // declared wrong on purpose
	s.TaskByID("hard").Difficulty = "hard"
	s.TaskByID("dead").Split = schemas.SplitHeldOut
	s.Lock.Tasks["flaky"] = schemas.TaskLock{Version: "1.0.0", Admission: &schemas.AdmissionRecord{
		ReferenceResults: []string{"pass", "fail", "pass"}, At: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}}
	s.Lock.Tasks["easy"] = schemas.TaskLock{Version: "1.0.0", Admission: &schemas.AdmissionRecord{ReferenceResults: []string{"pass", "pass", "pass"}}}
	return s
}

// fixture builds a record where each task shows one behaviour:
//   - easy: both configs pass 4 of 4 (saturated, declared hard: mismatch)
//   - hard: alpha 1 of 4, beta 0 of 4 (hard, declared hard: match)
//   - mixed: alpha 2 of 4, beta 4 of 4 (agent variance, never flakiness)
//   - flaky: one output passed then failed, admission reference inconsistent,
//     a same-version regrade flips a run, the oracle failed once
//   - dead: every run fails (held out)
//   - infra: aborted_infra retries, an infra_error and a timeout
func fixture() *fakeSource {
	f := newFakeSource()
	e1 := f.exp("e1", map[string]schemas.AgentConfig{"A": agentA, "B": agentB})
	for i := 0; i < 4; i++ {
		e1.run("easy", "A", schemas.OutcomePass, grade(true, fmt.Sprintf("ea%d", i)), 0)
		e1.run("easy", "B", schemas.OutcomePass, grade(true, fmt.Sprintf("eb%d", i)), 0)
		e1.run("hard", "A", outcomeOf(i == 0), grade(i == 0, fmt.Sprintf("ha%d", i)), 0)
		e1.run("hard", "B", schemas.OutcomeFail, grade(false, fmt.Sprintf("hb%d", i)), 0)
		e1.run("mixed", "A", outcomeOf(i%2 == 0), grade(i%2 == 0, fmt.Sprintf("ma%d", i)), 0)
		e1.run("mixed", "B", schemas.OutcomePass, grade(true, fmt.Sprintf("mb%d", i)), 0)
		e1.run("dead", "A", schemas.OutcomeFail, grade(false, fmt.Sprintf("da%d", i)), 0)
		e1.run("dead", "B", schemas.OutcomeFail, grade(false, fmt.Sprintf("db%d", i)), 0)
	}
	e1.run("flaky", "A", schemas.OutcomePass, grade(true, "same"), 0)
	e1.run("flaky", "B", schemas.OutcomeFail, grade(false, "same"), 0)
	flipped := e1.run("flaky", "A", schemas.OutcomePass, grade(true, "other"), 0)
	e1.run("infra", "A", schemas.OutcomePass, grade(true, "i1"), 2)
	e1.run("infra", "A", schemas.OutcomeInfraError, nil, 0)
	e1.run("infra", "B", schemas.OutcomeTimeout, grade(false, "i3"), 0)
	e1.run("infra", "B", schemas.OutcomeFail, grade(false, "i4"), 0)

	// A same-version regrade flips one flaky run; a new-grader regrade of an
	// easy run is a deliberate change and must not count.
	f.regrades["e1"] = []Regrade{{ID: "rg1", SuiteVersion: "1.0.0", GraderVersion: "g1"}, {ID: "rg2", GraderVersion: "g2"}, {ID: "regate", GraderVersion: ""}}
	for _, sr := range f.sealed["e1"][""] {
		r := sr
		r.GradeSource = "rg1"
		if r.Run.RunID == flipped {
			g := *sr.Grade
			g.Passed = false
			r.Grade = &g
		}
		f.sealed["e1"]["rg1"] = append(f.sealed["e1"]["rg1"], r)
		r2 := sr
		r2.GradeSource = "rg2"
		if sr.Grade != nil {
			g := *sr.Grade
			g.GraderVersion, g.Passed = "g2", !g.Passed
			r2.Grade = &g
		}
		f.sealed["e1"]["rg2"] = append(f.sealed["e1"]["rg2"], r2)
	}

	// Synthetic runs: a toy agent passing "hard" must not move its difficulty;
	// the oracle failing "flaky" is verifier evidence.
	e2 := f.exp("e2", map[string]schemas.AgentConfig{"A": toy, "B": orac})
	for i := 0; i < 5; i++ {
		e2.run("hard", "A", schemas.OutcomePass, grade(true, fmt.Sprintf("t%d", i)), 0)
	}
	e2.run("flaky", "B", schemas.OutcomeFail, grade(false, "ref"), 0)
	e2.run("easy", "B", schemas.OutcomePass, grade(true, "ref-easy"), 0)

	// Another suite and another task version are left out.
	e3 := f.exp("e3", map[string]schemas.AgentConfig{"A": agentA})
	e3.m.SuiteID = "other"
	e3.run("easy", "A", schemas.OutcomeFail, grade(false, "x"), 0)
	e4 := f.exp("e4", map[string]schemas.AgentConfig{"A": agentA})
	e4.run("easy", "A", schemas.OutcomeFail, grade(false, "y"), 0)
	e4.m.Slots[0].TaskVersion = "0.9.0"
	sr := f.sealed["e4"][""][0]
	sr.Slot.TaskVersion = "0.9.0"
	f.sealed["e4"][""][0] = sr
	return f
}

func computeFixture(t *testing.T) *schemas.SuiteHealth {
	t.Helper()
	q := map[string]schemas.TaskQuarantine{"dead": {Reason: "broken env", Source: schemas.QuarantineManual}}
	h, err := Compute(context.Background(), fixture(), testSuite(), q, Options{Now: func() time.Time { return time.Unix(0, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func taskOf(t *testing.T, h *schemas.SuiteHealth, id string) schemas.TaskHealth {
	t.Helper()
	for _, th := range h.Tasks {
		if th.Task == id {
			return th
		}
	}
	t.Fatalf("task %s missing", id)
	return schemas.TaskHealth{}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestComputeDifficulty(t *testing.T) {
	h := computeFixture(t)
	tests := []struct {
		task                     string
		runs, configs            int
		rate                     float64
		observed, match, discrim string
		spread                   float64
		wantFlags                []string
	}{
		{"easy", 8, 2, 1, "easy", "mismatch", "always_pass", 0,
			[]string{schemas.HealthDifficultyMismatch, schemas.HealthNoDiscrimination, schemas.HealthSaturated}},
		{"hard", 8, 2, 0.125, "hard", "match", "discriminating", 0.25, []string{}},
		{"mixed", 8, 2, 0.75, "easy", "undeclared", "discriminating", 0.5, []string{}},
		{"dead", 8, 2, 0, "hard", "undeclared", "always_fail", 0, []string{schemas.HealthDead, schemas.HealthNoDiscrimination}},
	}
	for _, tt := range tests {
		t.Run(tt.task, func(t *testing.T) {
			d := taskOf(t, h, tt.task).Difficulty
			if d.Runs != tt.runs || d.Configs != tt.configs || d.PassRate == nil || !near(*d.PassRate, tt.rate) {
				t.Fatalf("runs %d configs %d rate %v, want %d %d %v", d.Runs, d.Configs, d.PassRate, tt.runs, tt.configs, tt.rate)
			}
			if d.Observed != tt.observed || d.Match != tt.match || d.Discrimination != tt.discrim {
				t.Fatalf("observed %s match %s discrimination %s", d.Observed, d.Match, d.Discrimination)
			}
			if d.Spread == nil || !near(*d.Spread, tt.spread) {
				t.Fatalf("spread %v, want %v", d.Spread, tt.spread)
			}
			lo, hi, _ := Wilson(int(math.Round(tt.rate*float64(tt.runs))), tt.runs)
			if d.Interval == nil || !near(d.Interval[0], lo) || !near(d.Interval[1], hi) {
				t.Fatalf("interval %v, want [%v %v]", d.Interval, lo, hi)
			}
			if got := taskOf(t, h, tt.task).Flags; !slices.Equal(got, tt.wantFlags) {
				t.Fatalf("flags %v, want %v", got, tt.wantFlags)
			}
		})
	}
}

// TestComputeVarianceIsNotFlakiness pins the separation: a config that
// passes 2 of 4 is variance, and with no verifier evidence the task is not
// flaky.
func TestComputeVarianceIsNotFlakiness(t *testing.T) {
	h := computeFixture(t)
	m := taskOf(t, h, "mixed")
	if m.Verifier.Flaky || len(m.Verifier.Evidence) != 0 {
		t.Fatalf("agent variance reported as verifier flakiness: %+v", m.Verifier)
	}
	if m.Variance.Configs != 2 || m.Variance.Unstable != 1 || !near(m.Variance.MeanVariance, 0.125) {
		t.Fatalf("variance %+v, want 2 configs, 1 unstable, mean 0.125", m.Variance)
	}
	// 8 like-for-like regrade pairs, all agreeing.
	if m.Verifier.Checks != 8 {
		t.Fatalf("mixed checks %d, want 8", m.Verifier.Checks)
	}
}

func TestComputeVerifierFlakiness(t *testing.T) {
	h := computeFixture(t)
	f := taskOf(t, h, "flaky")
	if !f.Verifier.Flaky || !slices.Contains(f.Flags, schemas.HealthFlakyVerifier) {
		t.Fatalf("flaky task not flagged: %+v", f)
	}
	var kinds []string
	for _, e := range f.Verifier.Evidence {
		kinds = append(kinds, e.Kind)
	}
	slices.Sort(kinds)
	want := []string{schemas.EvidenceAdmissionRef, schemas.EvidenceOracleFailed, schemas.EvidenceRegradeDisagrees, schemas.EvidenceSameDiffDiffers}
	if !slices.Equal(kinds, want) {
		t.Fatalf("evidence kinds %v, want %v", kinds, want)
	}
	// checks: 3 same-version regrade pairs, 1 output group of 2, 1 oracle run, 1 admission
	if f.Verifier.Checks != 6 {
		t.Fatalf("checks %d, want 6", f.Verifier.Checks)
	}
	e := taskOf(t, h, "easy")
	if e.Verifier.Flaky {
		t.Fatalf("a new-grader regrade flipping verdicts is not flakiness: %+v", e.Verifier.Evidence)
	}
	// easy: 8 same-version regrade pairs, 1 passing oracle run, 1 all-pass admission
	if e.Verifier.Checks != 10 {
		t.Fatalf("easy checks %d, want 10", e.Verifier.Checks)
	}
	if got := FlakyTasks(h); !slices.Equal(got, []string{"flaky"}) {
		t.Fatalf("FlakyTasks = %v", got)
	}
	if lines := EvidenceLines(f.Verifier); len(lines) != 4 {
		t.Fatalf("evidence lines %v", lines)
	}
}

func TestComputeInfraAndExclusions(t *testing.T) {
	h := computeFixture(t)
	in := taskOf(t, h, "infra")
	want := schemas.InfraHealth{Attempts: 6, AbortedInfra: 2, InfraError: 1, Timeouts: 1, Rate: 0.5, TimeoutRate: 1.0 / 6}
	if in.Infra.Attempts != want.Attempts || in.Infra.AbortedInfra != 2 || in.Infra.InfraError != 1 || in.Infra.Timeouts != 1 ||
		!near(in.Infra.Rate, want.Rate) || !near(in.Infra.TimeoutRate, want.TimeoutRate) {
		t.Fatalf("infra %+v, want %+v", in.Infra, want)
	}
	if !slices.Contains(in.Flags, schemas.HealthInfraTrouble) || slices.Contains(in.Flags, schemas.HealthTimeouts) {
		t.Fatalf("infra flags %v", in.Flags)
	}
	// infra_error is not scored, so 3 scored runs: too few to label.
	if in.Difficulty.Runs != 3 || in.Difficulty.Observed != "unknown" || !slices.Contains(in.Flags, schemas.HealthInsufficientData) {
		t.Fatalf("infra difficulty %+v flags %v", in.Difficulty, in.Flags)
	}
	hard := taskOf(t, h, "hard")
	if hard.Difficulty.Runs != 8 {
		t.Fatalf("synthetic toy runs entered difficulty: %d runs", hard.Difficulty.Runs)
	}
	if h.Experiments != 3 {
		t.Fatalf("experiments %d, want 3 (other suite left out)", h.Experiments)
	}
	if !slices.Contains(h.Notes, "1 runs of other task versions were left out") {
		t.Fatalf("notes %v", h.Notes)
	}
	dead := taskOf(t, h, "dead")
	if dead.Split != schemas.SplitHeldOut || dead.Quarantine == nil || dead.Quarantine.Reason != "broken env" {
		t.Fatalf("dead split/quarantine %+v", dead)
	}
	if h.Counts[schemas.HealthFlakyVerifier] != 1 || h.Counts[schemas.HealthNoDiscrimination] != 2 {
		t.Fatalf("counts %v", h.Counts)
	}
}

func TestWilson(t *testing.T) {
	tests := []struct {
		x, n   int
		lo, hi float64
		ok     bool
	}{
		{0, 10, 0, 0.27753279986288, true},
		{10, 10, 0.72246720013712, 1, true},
		{5, 10, 0.23659309051316, 0.76340690948684, true},
		{1, 8, 0.02241749145006, 0.47088818221285, true},
		{0, 0, 0, 0, false},
		{3, 2, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d/%d", tt.x, tt.n), func(t *testing.T) {
			lo, hi, ok := Wilson(tt.x, tt.n)
			if ok != tt.ok || math.Abs(lo-tt.lo) > 1e-9 || math.Abs(hi-tt.hi) > 1e-9 {
				t.Fatalf("Wilson(%d, %d) = %v %v %v, want %v %v %v", tt.x, tt.n, lo, hi, ok, tt.lo, tt.hi, tt.ok)
			}
		})
	}
}

func TestDifficultyLabel(t *testing.T) {
	tests := []struct {
		rate          float64
		runs, configs int
		want          string
	}{
		{0.9, 10, 2, "easy"}, {0.7, 10, 2, "easy"}, {0.5, 10, 2, "medium"}, {0.3, 10, 2, "hard"},
		{0, 10, 3, "hard"}, {1, 5, 2, "unknown"}, {1, 10, 1, "unknown"},
	}
	for _, tt := range tests {
		if got := DifficultyLabel(tt.rate, tt.runs, tt.configs, Options{}); got != tt.want {
			t.Errorf("DifficultyLabel(%v, %d, %d) = %s, want %s", tt.rate, tt.runs, tt.configs, got, tt.want)
		}
	}
}

func TestComputeRequiresInputs(t *testing.T) {
	if _, err := Compute(context.Background(), nil, testSuite(), nil, Options{}); err == nil {
		t.Fatal("nil source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compute(ctx, fixture(), testSuite(), nil, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
}
