package leaderboard

import (
	"cmp"
	"context"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

func obs(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }

func unk() schemas.Value { return schemas.Unknown("no rows", schemas.EvidenceGateway) }

// runsWith builds graded runs from (success, metric value) pairs.
func runsWith(id string, success []float64, vals []schemas.Value) []gradedRun {
	out := make([]gradedRun, len(success))
	for i := range success {
		out[i] = gradedRun{rec: &record{}, success: success[i], metrics: map[string]schemas.Value{}}
		if i < len(vals) && vals[i].State != "" {
			out[i].metrics[id] = vals[i]
		}
	}
	return out
}

func TestSuccessValue(t *testing.T) {
	tests := []struct {
		name       string
		rates      map[string]float64
		wantState  schemas.ValueState
		wantV      float64
		wantBounds *[2]float64
	}{
		{name: "none", rates: map[string]float64{}, wantState: schemas.ValueUnknown},
		{name: "one task has no interval", rates: map[string]float64{"t1": 0.5}, wantState: schemas.ValueObserved, wantV: 0.5},
		{name: "constant rates", rates: map[string]float64{"t1": 1, "t2": 1}, wantState: schemas.ValueObserved, wantV: 1, wantBounds: &[2]float64{1, 1}},
		{name: "interval clipped to 0..1", rates: map[string]float64{"t1": 1, "t2": 0}, wantState: schemas.ValueObserved, wantV: 0.5, wantBounds: &[2]float64{0, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := successValue(tt.rates)
			if v.State != tt.wantState {
				t.Fatalf("state = %s, want %s", v.State, tt.wantState)
			}
			if x, ok := v.Float(); ok && math.Abs(x-tt.wantV) > 1e-12 {
				t.Errorf("v = %v, want %v", x, tt.wantV)
			}
			if !reflect.DeepEqual(v.Bounds, tt.wantBounds) {
				t.Errorf("bounds = %v, want %v", v.Bounds, tt.wantBounds)
			}
		})
	}
}

func TestReliability(t *testing.T) {
	tests := []struct {
		name       string
		tally      map[string]taskTally
		k          int
		wantState  schemas.ValueState
		wantV      float64
		wantReason string
	}{
		{name: "no task", tally: map[string]taskTally{}, k: 0, wantState: schemas.ValueUnknown, wantReason: "no graded run in scope"},
		{name: "one trial", tally: map[string]taskTally{"t1": {1, 1}}, k: 1, wantState: schemas.ValueUnknown,
			wantReason: "pass^k needs 2 or more trials per task; its experiments ran 1"},
		{name: "a task short of k", tally: map[string]taskTally{"t1": {2, 3}, "t2": {1, 1}}, k: 2, wantState: schemas.ValueUnknown,
			wantReason: "pass^2 needs 2 graded runs on every task; t2 has 1"},
		{name: "all pass", tally: map[string]taskTally{"t1": {2, 2}, "t2": {3, 3}}, k: 2, wantState: schemas.ValueObserved, wantV: 1, wantReason: "pass^2"},
		// t1: C(2,2)/C(3,2) = 1/3; t2: 0.
		{name: "unbiased pass^k", tally: map[string]taskTally{"t1": {2, 3}, "t2": {1, 2}}, k: 2, wantState: schemas.ValueObserved, wantV: 1.0 / 6, wantReason: "pass^2"},
		// Pooled runs beyond k still enter: C(3,3)/C(4,3) = 1/4.
		{name: "more runs than k", tally: map[string]taskTally{"t1": {3, 4}}, k: 3, wantState: schemas.ValueObserved, wantV: 0.25, wantReason: "pass^3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := reliability(tt.tally, tt.k)
			if v.State != tt.wantState || v.Reason != tt.wantReason {
				t.Fatalf("got %s %q, want %s %q", v.State, v.Reason, tt.wantState, tt.wantReason)
			}
			if x, ok := v.Float(); ok && math.Abs(x-tt.wantV) > 1e-12 {
				t.Errorf("v = %v, want %v", x, tt.wantV)
			}
		})
	}
}

func TestDefaultK(t *testing.T) {
	run := func(exp string, trials int) gradedRun {
		e := &expInfo{id: exp, trials: map[string]int{trialKey("A", "clean"): trials}}
		return gradedRun{rec: &record{exp: e, slot: schemas.Slot{Arm: "A", Condition: "clean"}}}
	}
	tests := []struct {
		name string
		runs []gradedRun
		want int
	}{
		{name: "no runs", runs: nil, want: 0},
		{name: "one trial everywhere", runs: []gradedRun{run("e1", 1), run("e2", 1)}, want: 1},
		{name: "trials unknown", runs: []gradedRun{run("e1", 0)}, want: 1},
		{name: "fewest among repeating experiments", runs: []gradedRun{run("e1", 3), run("e2", 2), run("e1", 3)}, want: 2},
		{name: "a single-trial pilot does not set k", runs: []gradedRun{run("e1", 1), run("e2", 3)}, want: 3},
		{name: "capped at 3", runs: []gradedRun{run("e1", 5), run("e2", 8)}, want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultK(tt.runs); got != tt.want {
				t.Errorf("defaultK = %d, want %d", got, tt.want)
			}
		})
	}
}

// kSource has config h with 3 trials per task in e_rep (t1: 2 of 3, t2: 3
// of 3) and a single-trial pilot e_pilot that passed t1 once more.
func kSource() *fakeSource {
	f := newFake()
	f.exp("e_rep", "s@1", 1, map[string]schemas.ArmManifest{"A": arm("rep", "h", "command")})
	f.exp("e_pilot", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("pilot", "h", "command")})
	for trial := 1; trial <= 3; trial++ {
		f.pass("e_rep", "t2", "A", "clean", trial, 0.1)
		if trial < 3 {
			f.pass("e_rep", "t1", "A", "clean", trial, 0.1)
		} else {
			f.fail("e_rep", "t1", "A", "clean", trial, 0.1)
		}
	}
	f.pass("e_pilot", "t1", "A", "clean", 1, 0.1)
	return f
}

func TestBuildReliabilityK(t *testing.T) {
	tests := []struct {
		name       string
		k          int
		wantTrials int
		wantState  schemas.ValueState
		wantV      float64
		wantReason string
	}{
		// t1 pooled 3 of 4: C(3,3)/C(4,3) = 1/4; t2: 1.
		{name: "default is the repeating experiment's trials", wantTrials: 3, wantState: schemas.ValueObserved, wantV: 0.625, wantReason: "pass^3"},
		// t1: C(3,2)/C(4,2) = 1/2; t2: 1.
		{name: "query k", k: 2, wantTrials: 2, wantState: schemas.ValueObserved, wantV: 0.75, wantReason: "pass^2"},
		{name: "query k above the runs", k: 4, wantTrials: 4, wantState: schemas.ValueUnknown, wantReason: "pass^4 needs 4 graded runs on every task; t2 has 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := buildOrFail(t, kSource(), Query{K: tt.k, Now: t0})
			e := lb.Entries[0]
			if e.Trials != tt.wantTrials || e.Reliability.State != tt.wantState || e.Reliability.Reason != tt.wantReason {
				t.Fatalf("trials %d reliability %+v, want %d %s %q", e.Trials, e.Reliability, tt.wantTrials, tt.wantState, tt.wantReason)
			}
			if x, ok := e.Reliability.Float(); ok && math.Abs(x-tt.wantV) > 1e-12 {
				t.Errorf("reliability = %v, want %v", x, tt.wantV)
			}
		})
	}
}

func TestCollectTrials(t *testing.T) {
	f := newFake()
	m := f.exp("e", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h_a", "command"), "B": arm("b", "h_b", "command")})
	// The manifest plans A for 2 trials; only trial 1 ran. It lists no B slots.
	m.Slots = []schemas.Slot{
		{SlotID: "p1", Arm: "A", Task: "t1", Condition: "clean", Trial: 1},
		{SlotID: "p2", Arm: "A", Task: "t1", Condition: "clean", Trial: 2},
	}
	f.pass("e", "t1", "A", "clean", 1, 0.1)
	f.pass("e", "t1", "B", "clean", 3, 0.1)
	recs, err := collect(context.Background(), f)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	want := map[string]int{trialKey("A", "clean"): 2, trialKey("B", "clean"): 3}
	if got := recs[0].exp.trials; !reflect.DeepEqual(got, want) {
		t.Errorf("trials = %v, want %v", got, want)
	}
}

func TestAggregate(t *testing.T) {
	na := schemas.NotApplicable()
	tests := []struct {
		name       string
		metric     string // default steps
		vals       []schemas.Value
		wantState  schemas.ValueState
		wantV      float64
		wantReason string
	}{
		{name: "median of observed", vals: []schemas.Value{obs(1), obs(9), obs(4)}, wantState: schemas.ValueObserved, wantV: 4},
		{name: "unknown is never zero", vals: []schemas.Value{obs(10), unk(), obs(20)}, wantState: schemas.ValueObserved, wantV: 15, wantReason: "1 of 3 runs unknown"},
		{name: "missing counts as unknown", vals: []schemas.Value{obs(10), {}}, wantState: schemas.ValueObserved, wantV: 10, wantReason: "1 of 2 runs unknown"},
		{name: "all unknown", vals: []schemas.Value{unk(), unk()}, wantState: schemas.ValueUnknown, wantReason: "unknown in all 2 runs"},
		{name: "not applicable left out", vals: []schemas.Value{na, obs(3)}, wantState: schemas.ValueObserved, wantV: 3},
		{name: "all not applicable", vals: []schemas.Value{na, na}, wantState: schemas.ValueNotApplicable},
		{name: "false completion: a passing run counts as 0", metric: MetricFalseCompletion, vals: []schemas.Value{na, na, obs(1), obs(0)},
			wantState: schemas.ValueObserved, wantV: 0.25},
		{name: "false completion: every run passed", metric: MetricFalseCompletion, vals: []schemas.Value{na, na}, wantState: schemas.ValueObserved, wantV: 0},
		{name: "false completion: unknown claims left out", metric: MetricFalseCompletion, vals: []schemas.Value{na, unk(), obs(1)},
			wantState: schemas.ValueObserved, wantV: 0.5, wantReason: "1 of 3 runs unknown"},
		{name: "safe success: not applicable left out", metric: MetricSafeSuccess, vals: []schemas.Value{na, obs(1)}, wantState: schemas.ValueObserved, wantV: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := cmp.Or(tt.metric, MetricSteps)
			f := stats.Median
			if slices.Contains(rateMetrics, id) {
				f = stats.Mean
			}
			v := aggregate(id, runsWith(id, make([]float64, len(tt.vals)), tt.vals), f)
			if v.State != tt.wantState || v.Reason != tt.wantReason {
				t.Fatalf("got %s %q, want %s %q", v.State, v.Reason, tt.wantState, tt.wantReason)
			}
			if x, ok := v.Float(); ok && x != tt.wantV {
				t.Errorf("v = %v, want %v", x, tt.wantV)
			}
		})
	}
}

func TestCostPerSuccess(t *testing.T) {
	tests := []struct {
		name       string
		success    []float64
		costs      []schemas.Value
		wantState  schemas.ValueState
		wantV      float64
		wantReason string
	}{
		{name: "failures count against it", success: []float64{1, 0, 1}, costs: []schemas.Value{obs(0.2), obs(0.2), obs(0.2)}, wantState: schemas.ValueObserved, wantV: 0.3},
		{name: "no success", success: []float64{0, 0}, costs: []schemas.Value{obs(1), obs(1)}, wantState: schemas.ValueNotApplicable, wantReason: "undefined (0 of 2)"},
		{name: "a cost unknown", success: []float64{1, 1}, costs: []schemas.Value{obs(1), unk()}, wantState: schemas.ValueUnknown, wantReason: "cost unknown in 1 of 2 runs"},
		{name: "no runs", wantState: schemas.ValueUnknown, wantReason: "no graded run in scope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := costPerSuccess(runsWith(MetricCostUSD, tt.success, tt.costs))
			if v.State != tt.wantState || v.Reason != tt.wantReason {
				t.Fatalf("got %s %q, want %s %q", v.State, v.Reason, tt.wantState, tt.wantReason)
			}
			if x, ok := v.Float(); ok && math.Abs(x-tt.wantV) > 1e-12 {
				t.Errorf("v = %v, want %v", x, tt.wantV)
			}
		})
	}
}

func TestResilience(t *testing.T) {
	run := func(task string, y float64) gradedRun {
		return gradedRun{rec: &record{slot: schemas.Slot{Task: task}}, success: y}
	}
	byCond := map[string][]gradedRun{
		"clean":        {run("t1", 1), run("t2", 1)},
		"model-outage": {run("t1", 0), run("t1", 1), run("t2", 0), run("t9", 0)},
		"test-crash":   {run("t9", 1)},
	}
	got := resilience(byCond, map[string]float64{"t1": 1, "t2": 1})
	mo := got["model-outage"]
	if v, _ := mo.Float(); v != -0.75 || mo.Reason != "2 tasks" {
		t.Errorf("model-outage = %+v, want -0.75 over 2 tasks (t9 never ran clean)", mo)
	}
	if tc := got["test-crash"]; tc.State != schemas.ValueUnknown {
		t.Errorf("test-crash = %+v, want unknown: no task in common with clean", tc)
	}
	if _, ok := got["clean"]; ok {
		t.Error("resilience lists clean against itself")
	}
}

func TestBuildEntriesScope(t *testing.T) {
	f := newFake()
	f.exp("e", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h_a", "command"), "B": arm("b", "h_b", "command")})
	f.pass("e", "t1", "A", "clean", 1, 0.1)
	f.pass("e", "t2", "A", "clean", 1, 0.1)
	f.fail("e", "t1", "B", "clean", 1, 0.1)
	f.pass("e", "t3", "B", "clean", 1, 0.1)
	tests := []struct {
		scope     string
		wantTasks map[string]int
	}{
		{scope: ScopeAll, wantTasks: map[string]int{"h_a": 2, "h_b": 2}},
		{scope: ScopeCommon, wantTasks: map[string]int{"h_a": 1, "h_b": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			lb := buildOrFail(t, f, Query{Scope: tt.scope, Now: t0})
			if !reflect.DeepEqual(lb.CommonTasks, []string{"t1"}) {
				t.Errorf("common = %v, want [t1]", lb.CommonTasks)
			}
			got := map[string]int{}
			for _, e := range lb.Entries {
				got[e.ConfigHash] = e.Tasks
			}
			if !reflect.DeepEqual(got, tt.wantTasks) {
				t.Errorf("tasks = %v, want %v", got, tt.wantTasks)
			}
		})
	}
}
