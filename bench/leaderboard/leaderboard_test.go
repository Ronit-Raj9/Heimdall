package leaderboard

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// This file holds the in-memory Source every test in the package uses.

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// fakeSource is an in-memory Source and RegradeSource.
type fakeSource struct {
	manifests  map[string]*schemas.Manifest
	runs       map[string][]Run
	files      map[string]map[string][]byte // run id -> name -> bytes
	decisions  map[string]schemas.Decision
	regrades   map[string][]Regrade           // exp id -> regrades
	regraded   map[string][]schemas.SealedRun // exp id + "/" + regrade id -> runs
	fileErr    error                          // returned by every RunFile call when set
	regradeErr error                          // returned by every RegradedRuns call when set
}

func newFake() *fakeSource {
	return &fakeSource{manifests: map[string]*schemas.Manifest{}, runs: map[string][]Run{},
		files: map[string]map[string][]byte{}, decisions: map[string]schemas.Decision{},
		regrades: map[string][]Regrade{}, regraded: map[string][]schemas.SealedRun{}}
}

func (f *fakeSource) Experiments() []string {
	var ids []string
	for id := range f.manifests {
		ids = append(ids, id)
	}
	return ids
}

func (f *fakeSource) Manifest(expID string) (*schemas.Manifest, bool) {
	m, ok := f.manifests[expID]
	return m, ok
}

func (f *fakeSource) Runs(expID string) []Run { return slices.Clone(f.runs[expID]) }

func (f *fakeSource) RunFile(runID, name string) ([]byte, error) {
	if f.fileErr != nil {
		return nil, f.fileErr
	}
	if b, ok := f.files[runID][name]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("run %s file %s: %w", runID, name, fs.ErrNotExist)
}

func (f *fakeSource) LatestDecision(expID string) (schemas.Decision, bool) {
	d, ok := f.decisions[expID]
	return d, ok
}

func (f *fakeSource) Regrades(_ context.Context, expID string) ([]Regrade, error) {
	return slices.Clone(f.regrades[expID]), nil
}

func (f *fakeSource) RegradedRuns(_ context.Context, expID, regradeID string) ([]schemas.SealedRun, error) {
	if f.regradeErr != nil {
		return nil, f.regradeErr
	}
	runs, ok := f.regraded[expID+"/"+regradeID]
	if !ok {
		return nil, fmt.Errorf("regrade %s: %w", regradeID, schemas.ErrNotFound)
	}
	return slices.Clone(runs), nil
}

// arm is a manifest arm with a live command agent unless adapter says otherwise.
func arm(label, hash, adapter string) schemas.ArmManifest {
	return schemas.ArmManifest{Label: label, ConfigHash: hash,
		Config: schemas.AgentConfig{ID: "agent-" + hash, Adapter: adapter, Model: "model-" + hash}}
}

// exp adds an experiment of suite created hours after t0.
func (f *fakeSource) exp(id, suite string, hours int, arms map[string]schemas.ArmManifest) *schemas.Manifest {
	sid, ver, _ := cutSuite(suite)
	m := &schemas.Manifest{ExpID: id, SuiteID: sid, SuiteVersion: ver, CreatedAt: t0.Add(time.Duration(hours) * time.Hour), Arms: arms}
	f.manifests[id] = m
	return m
}

func cutSuite(s string) (string, string, bool) {
	for i := range s {
		if s[i] == '@' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// run adds one run. metrics is the metrics.json body, or "" for none.
func (f *fakeSource) run(expID, task, armID, cond string, trial, attempt int, o schemas.Outcome, sealed bool, metrics string) string {
	slotID := fmt.Sprintf("s_%s_%s_%s_%s_%d", expID, task, armID, cond, trial)
	runID := fmt.Sprintf("r_%s_%s_%s_%s_%d_%d", expID, task, armID, cond, trial, attempt)
	f.runs[expID] = append(f.runs[expID], Run{
		Slot: schemas.Slot{SlotID: slotID, ExpID: expID, Task: task, Arm: armID, Condition: cond, Trial: trial, Counted: armID != "compat"},
		Run:  schemas.Run{RunID: runID, SlotID: slotID, ExpID: expID, Attempt: attempt, Outcome: o}, Sealed: sealed,
	})
	f.files[runID] = map[string][]byte{}
	if metrics != "" {
		f.files[runID][fileMetrics] = []byte(metrics)
	}
	return runID
}

// pass and fail add a sealed graded run with verified_success and a cost.
func (f *fakeSource) pass(expID, task, armID, cond string, trial int, cost float64) string {
	return f.run(expID, task, armID, cond, trial, 1, schemas.OutcomePass, true, metricsJSON(1, cost))
}

func (f *fakeSource) fail(expID, task, armID, cond string, trial int, cost float64) string {
	return f.run(expID, task, armID, cond, trial, 1, schemas.OutcomeFail, true, metricsJSON(0, cost))
}

func metricsJSON(y, cost float64) string {
	return fmt.Sprintf(`{"verified_success":{"state":"observed","v":%v,"evidence":"outcome"},`+
		`"cost_usd":{"state":"observed","v":%v,"evidence":"gateway"},`+
		`"steps":{"state":"observed","v":4,"evidence":"gateway"}}`, y, cost)
}

// buildOrFail builds a board or fails the test.
func buildOrFail(t *testing.T, src Source, q Query) *schemas.Leaderboard {
	t.Helper()
	lb, err := Build(context.Background(), src, q)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return lb
}

func entryHashes(lb *schemas.Leaderboard) []string {
	var out []string
	for _, e := range lb.Entries {
		out = append(out, e.ConfigHash)
	}
	return out
}

func TestQueryNormalize(t *testing.T) {
	tests := []struct {
		name    string
		in      Query
		want    Query
		wantErr bool
	}{
		{name: "defaults", in: Query{Now: t0},
			want: Query{Condition: "clean", Scope: "all", Synthetic: "exclude", GradeSource: "original", Alpha: 0.05, Now: t0}},
		{name: "explicit", in: Query{Suite: "coding-core@1.0.0", Condition: "model-outage", Scope: "common", Synthetic: "only", GradeSource: " latest ", K: 2, Alpha: 0.1, Now: t0},
			want: Query{Suite: "coding-core@1.0.0", Condition: "model-outage", Scope: "common", Synthetic: "only", GradeSource: "latest", K: 2, Alpha: 0.1, Now: t0}},
		{name: "suite without version", in: Query{Suite: "coding-core"}, wantErr: true},
		{name: "suite with slash", in: Query{Suite: "../x@1"}, wantErr: true},
		{name: "condition with space", in: Query{Condition: "a b"}, wantErr: true},
		{name: "bad scope", in: Query{Scope: "some"}, wantErr: true},
		{name: "bad synthetic", in: Query{Synthetic: "include"}, wantErr: true},
		{name: "alpha out of range", in: Query{Alpha: 1.5}, wantErr: true},
		{name: "grade source is not a regrade id", in: Query{GradeSource: "rg_gates_1_1_0"}, wantErr: true},
		{name: "grade source newest", in: Query{GradeSource: "newest"}, wantErr: true},
		{name: "k of 1 is the success rate", in: Query{K: 1}, wantErr: true},
		{name: "negative k", in: Query{K: -2}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.Normalize()
			if tt.wantErr {
				if !errors.Is(err, ErrBadQuery) {
					t.Fatalf("err = %v, want ErrBadQuery", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// boardSource has two live configs over two experiments of suite s@2, an
// older suite s@1, a scripted toy config, and runs to exclude.
func boardSource() *fakeSource {
	f := newFake()
	f.exp("e_old", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("old", "h_old", "command")})
	f.pass("e_old", "t1", "A", "clean", 1, 0.1)

	f.exp("e_one", "s@2", 1, map[string]schemas.ArmManifest{"A": arm("alpha one", "h_a", "command"), "B": arm("beta", "h_b", "command")})
	f.exp("e_two", "s@2", 2, map[string]schemas.ArmManifest{"A": arm("alpha two", "h_a", "command")})
	for _, task := range []string{"t1", "t2"} {
		f.pass("e_one", task, "A", "clean", 1, 0.1)
		f.pass("e_two", task, "A", "clean", 1, 0.1)
		f.fail("e_one", task, "B", "clean", 1, 0.2)
	}
	f.pass("e_one", "t3", "B", "clean", 1, 0.2)
	f.fail("e_one", "t1", "A", "model-outage", 1, 0.1)
	// Left out: an infra error, a run in flight, a retried slot counts its second attempt.
	f.run("e_one", "t3", "A", "clean", 1, 1, schemas.OutcomeInfraError, true, "")
	f.run("e_two", "t3", "A", "clean", 1, 1, schemas.OutcomePass, false, "")
	f.run("e_two", "t4", "A", "clean", 1, 1, schemas.OutcomeAbortedInfra, true, "")
	f.run("e_two", "t4", "A", "clean", 1, 2, schemas.OutcomeFail, true, metricsJSON(0, 0.1))

	f.exp("e_toy", "s@2", 3, map[string]schemas.ArmManifest{"A": arm("toy", "h_toy", "scripted")})
	f.pass("e_toy", "t1", "A", "clean", 1, 0.01)

	f.exp("e_bad", "s@2", 4, map[string]schemas.ArmManifest{"A": arm("broken", "h_a", "command")})
	f.pass("e_bad", "t1", "A", "clean", 1, 0.1)
	f.decisions["e_bad"] = schemas.DecisionInvalid
	f.decisions["e_one"] = schemas.DecisionInconclusive
	return f
}

func TestBuild(t *testing.T) {
	tests := []struct {
		name         string
		q            Query
		wantSuite    string
		wantSuites   []string
		wantConds    []string
		wantEntries  []string
		wantExcluded []schemas.LeaderboardExclusion
		wantCommon   []string
	}{
		{
			name: "defaults: newest suite, clean, live", q: Query{Now: t0},
			wantSuite: "s@2", wantSuites: []string{"s@2", "s@1"}, wantConds: []string{"clean", "model-outage"},
			wantEntries: []string{"h_a", "h_b"}, wantCommon: []string{"t1", "t2"},
			wantExcluded: []schemas.LeaderboardExclusion{
				{Reason: "experiment verdict invalid", Runs: 1, Experiments: []string{"e_bad"}},
				{Reason: "infra_error", Runs: 1, Experiments: []string{"e_one"}},
				{Reason: "not sealed", Runs: 1, Experiments: []string{"e_two"}},
			},
		},
		{
			name: "synthetic only", q: Query{Synthetic: "only", Now: t0},
			wantSuite: "s@2", wantSuites: []string{"s@2"}, wantConds: []string{"clean"},
			wantEntries: []string{"h_toy"}, wantCommon: []string{"t1"}, wantExcluded: []schemas.LeaderboardExclusion{},
		},
		{
			name: "older suite", q: Query{Suite: "s@1", Now: t0},
			wantSuite: "s@1", wantSuites: []string{"s@2", "s@1"}, wantConds: []string{"clean"},
			wantEntries: []string{"h_old"}, wantCommon: []string{"t1"}, wantExcluded: []schemas.LeaderboardExclusion{},
		},
		{
			name: "fault condition", q: Query{Condition: "model-outage", Now: t0},
			wantSuite: "s@2", wantSuites: []string{"s@2", "s@1"}, wantConds: []string{"clean", "model-outage"},
			wantEntries: []string{"h_a"}, wantCommon: []string{"t1"}, wantExcluded: []schemas.LeaderboardExclusion{},
		},
		{
			name: "unknown suite is an empty board", q: Query{Suite: "nope@9", Now: t0},
			wantSuite: "nope@9", wantSuites: []string{"s@2", "s@1"}, wantConds: []string{},
			wantEntries: nil, wantCommon: []string{}, wantExcluded: []schemas.LeaderboardExclusion{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb, err := Build(context.Background(), boardSource(), tt.q)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if lb.Suite != tt.wantSuite || !reflect.DeepEqual(lb.Suites, tt.wantSuites) || !reflect.DeepEqual(lb.Conditions, tt.wantConds) {
				t.Errorf("suite %q suites %v conditions %v", lb.Suite, lb.Suites, lb.Conditions)
			}
			if got := entryHashes(lb); !reflect.DeepEqual(got, tt.wantEntries) {
				t.Errorf("entries = %v, want %v", got, tt.wantEntries)
			}
			if !reflect.DeepEqual(lb.Excluded, tt.wantExcluded) {
				t.Errorf("excluded = %+v, want %+v", lb.Excluded, tt.wantExcluded)
			}
			if !reflect.DeepEqual(lb.CommonTasks, tt.wantCommon) {
				t.Errorf("common = %v, want %v", lb.CommonTasks, tt.wantCommon)
			}
			if !lb.GeneratedAt.Equal(t0) || lb.SortMetric != "verified_success" || lb.Alpha != 0.05 {
				t.Errorf("generated %v sort %q alpha %v", lb.GeneratedAt, lb.SortMetric, lb.Alpha)
			}
			for _, e := range lb.Entries {
				if e.Synthetic != (lb.Synthetic == SyntheticOnly) {
					t.Errorf("entry %s synthetic = %v on a %s board", e.ConfigHash, e.Synthetic, lb.Synthetic)
				}
			}
		})
	}
}

func TestBuildEntryFields(t *testing.T) {
	lb, err := Build(context.Background(), boardSource(), Query{Now: t0})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	a := lb.Entries[0]
	// h_a: t1 and t2 pass in e_one and e_two, t4 fails on its second attempt.
	if a.Rank != 1 || a.Tier != 1 || a.TierReason != "leader" || a.PVsLeader != nil {
		t.Errorf("leader rank %d tier %d reason %q p %v", a.Rank, a.Tier, a.TierReason, a.PVsLeader)
	}
	if a.Label != "alpha two" || a.Agent != "agent-h_a" || a.Model != "model-h_a" {
		t.Errorf("identity = %q %q %q, want the newest contributing experiment's", a.Label, a.Agent, a.Model)
	}
	if !reflect.DeepEqual(a.Experiments, []string{"e_two", "e_one"}) {
		t.Errorf("experiments = %v", a.Experiments)
	}
	if a.Tasks != 3 || a.Runs != 5 || a.Trials != 1 || a.Passes != (schemas.Ratio{Num: 4, Den: 5}) {
		t.Errorf("tasks %d runs %d trials %d passes %+v", a.Tasks, a.Runs, a.Trials, a.Passes)
	}
	if v, _ := a.Success.Float(); math.Abs(v-2.0/3) > 1e-12 {
		t.Errorf("success = %v, want 2/3 (t1 1, t2 1, t4 0)", v)
	}
	if a.Reliability.State != schemas.ValueUnknown {
		t.Errorf("reliability = %+v, want unknown with one trial on t4", a.Reliability)
	}
	if v, _ := a.Metrics[MetricCostPerSuccess].Float(); math.Abs(v-0.125) > 1e-12 {
		t.Errorf("cost per success = %v, want 0.125", v)
	}
	r, ok := a.Resilience["model-outage"]
	if v, _ := r.Float(); !ok || v != -1 || r.Reason != "1 task" {
		t.Errorf("resilience = %+v, want -1 over 1 task", r)
	}
	b := lb.Entries[1]
	if b.Tier != 1 || b.PVsLeader == nil || b.TierReason != "too few common tasks (2) to separate" {
		t.Errorf("second tier %d p %v reason %q", b.Tier, b.PVsLeader, b.TierReason)
	}
	if len(b.Resilience) != 0 {
		t.Errorf("second resilience = %v, want none: it never ran a fault", b.Resilience)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	first, err := Build(context.Background(), boardSource(), Query{Now: t0})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for range 5 {
		again, err := Build(context.Background(), boardSource(), Query{Now: t0})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("two builds differ:\n%+v\n%+v", first, again)
		}
	}
}

func TestBuildErrors(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	broken := boardSource()
	broken.fileErr = errors.New("disk on fire")
	corrupt := boardSource()
	corrupt.files["r_e_one_t1_A_clean_1_1"][fileMetrics] = []byte(`{not json`)
	tests := []struct {
		name string
		ctx  context.Context
		src  *fakeSource
		q    Query
		bad  bool
	}{
		{name: "bad query", ctx: context.Background(), src: boardSource(), q: Query{Scope: "x"}, bad: true},
		{name: "cancelled", ctx: cancelled, src: boardSource()},
		{name: "unreadable file", ctx: context.Background(), src: broken},
		{name: "corrupt metrics", ctx: context.Background(), src: corrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(tt.ctx, tt.src, tt.q)
			if err == nil {
				t.Fatal("Build succeeded, want an error")
			}
			if errors.Is(err, ErrBadQuery) != tt.bad {
				t.Errorf("err = %v, bad query %v", err, tt.bad)
			}
		})
	}
}

func TestCountedRunSelection(t *testing.T) {
	exp := &expInfo{id: "e", m: &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": arm("a", "h", "command")}}}
	run := func(attempt int, o schemas.Outcome, sealed bool) Run {
		return Run{Slot: schemas.Slot{SlotID: "s", Arm: "A"}, Run: schemas.Run{RunID: fmt.Sprint("r", attempt), Attempt: attempt, Outcome: o}, Sealed: sealed}
	}
	tests := []struct {
		name       string
		runs       []Run
		wantRun    string
		wantSealed bool
	}{
		{name: "one sealed", runs: []Run{run(1, schemas.OutcomePass, true)}, wantRun: "r1", wantSealed: true},
		{name: "retry after aborted", runs: []Run{run(2, schemas.OutcomeFail, true), run(1, schemas.OutcomeAbortedInfra, true)}, wantRun: "r2", wantSealed: true},
		{name: "retry in flight", runs: []Run{run(1, schemas.OutcomeAbortedInfra, true), run(2, "", false)}, wantRun: "r2", wantSealed: false},
		{name: "retries spent", runs: []Run{run(1, schemas.OutcomeAbortedInfra, true), run(2, schemas.OutcomeAbortedInfra, true)}, wantRun: "r2", wantSealed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newRecord(exp, tt.runs)
			if rec.run.RunID != tt.wantRun || rec.sealed != tt.wantSealed || rec.hash != "h" {
				t.Errorf("got %s sealed %v hash %s", rec.run.RunID, rec.sealed, rec.hash)
			}
			if rec.counted > 1 {
				t.Errorf("counted %d, want at most 1", rec.counted)
			}
		})
	}
	dup := newRecord(exp, []Run{run(1, schemas.OutcomePass, true), run(2, schemas.OutcomeFail, true)})
	if dup.run.RunID != "r1" || dup.counted != 2 || excludeReason(dup) != ReasonDuplicateSlot {
		t.Errorf("two counted runs: got %s counted %d reason %q", dup.run.RunID, dup.counted, excludeReason(dup))
	}
	compat := &expInfo{id: "e", m: &schemas.Manifest{Compatibility: &schemas.ArmManifest{ConfigHash: "h_compat"}}}
	rec := newRecord(compat, []Run{{Slot: schemas.Slot{Arm: "compat"}, Sealed: true}})
	if rec == nil || rec.hash != "h_compat" {
		t.Errorf("compat record = %+v, want the compatibility config hash", rec)
	}
	if rec := newRecord(&expInfo{m: &schemas.Manifest{}}, []Run{{Slot: schemas.Slot{Arm: "X"}}}); rec != nil {
		t.Errorf("record without a config hash = %+v, want nil", rec)
	}
}
