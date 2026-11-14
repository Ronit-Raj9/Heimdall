package leaderboard

import (
	"math"
	"reflect"
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
		name      string
		tally     map[string]taskTally
		k         int
		wantState schemas.ValueState
		wantV     float64
	}{
		{name: "one trial", tally: map[string]taskTally{"t1": {1, 1}}, k: 1, wantState: schemas.ValueUnknown},
		{name: "all pass", tally: map[string]taskTally{"t1": {2, 2}, "t2": {3, 3}}, k: 2, wantState: schemas.ValueObserved, wantV: 1},
		// t1: C(2,2)/C(3,2) = 1/3; t2: 0.
		{name: "unbiased pass^k", tally: map[string]taskTally{"t1": {2, 3}, "t2": {1, 2}}, k: 2, wantState: schemas.ValueObserved, wantV: 1.0 / 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := reliability(tt.tally, tt.k)
			if v.State != tt.wantState {
				t.Fatalf("state = %s (%s), want %s", v.State, v.Reason, tt.wantState)
			}
			if x, ok := v.Float(); ok && math.Abs(x-tt.wantV) > 1e-12 {
				t.Errorf("v = %v, want %v", x, tt.wantV)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	na := schemas.NotApplicable()
	tests := []struct {
		name       string
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := aggregate(MetricSteps, runsWith(MetricSteps, make([]float64, len(tt.vals)), tt.vals), stats.Median)
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
