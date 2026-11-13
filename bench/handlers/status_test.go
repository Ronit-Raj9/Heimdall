package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBuildStatus(t *testing.T) {
	idx := testIndex()
	m := idx.manifests[testExp]
	runs := idx.Runs(testExp)
	edit := func(f func(r []RunView) []RunView) []RunView {
		return f(append([]RunView(nil), runs...))
	}
	tests := []struct {
		name   string
		runs   []RunView
		active bool
		state  schemas.ExperimentState
		slots  schemas.SlotProgress
	}{
		{"no runs", nil, false, schemas.ExperimentCreatedState, schemas.SlotProgress{Total: 4}},
		{"accepted, no runs yet", nil, true, schemas.ExperimentRunning, schemas.SlotProgress{Total: 4}},
		{"all sealed", runs, false, schemas.ExperimentDone, schemas.SlotProgress{Total: 4, Done: 4}},
		{"all sealed, still analyzing", runs, true, schemas.ExperimentAnalyzing, schemas.SlotProgress{Total: 4, Done: 4}},
		{"one in flight", edit(func(r []RunView) []RunView {
			r[0].Sealed = false
			return r
		}), false, schemas.ExperimentRunning, schemas.SlotProgress{Total: 4, Done: 3, Running: 1}},
		{"infra failure is failed, not done", edit(func(r []RunView) []RunView {
			r[1].Run.Outcome = schemas.OutcomeInfraError
			return r
		}), false, schemas.ExperimentDone, schemas.SlotProgress{Total: 4, Done: 3, Failed: 1}},
		{"retry after aborted_infra in flight", edit(func(r []RunView) []RunView {
			r[0].Run.Outcome = schemas.OutcomeAbortedInfra
			retry := r[0]
			retry.Sealed, retry.Run.Attempt, retry.Run.RunID, retry.Run.Outcome = false, 2, "r_retry", ""
			return append(r, retry)
		}), false, schemas.ExperimentRunning, schemas.SlotProgress{Total: 4, Done: 3, Running: 1}},
		{"stopped with slots left", runs[:2], false, schemas.ExperimentFailed, schemas.SlotProgress{Total: 4, Done: 2}},
		{"stopped with slots left, launcher active", runs[:2], true, schemas.ExperimentRunning, schemas.SlotProgress{Total: 4, Done: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := BuildStatus(m, tt.runs, StatusFacts{Active: tt.active})
			if st.State != tt.state || st.Slots != tt.slots {
				t.Fatalf("state %s slots %+v, want %s %+v", st.State, st.Slots, tt.state, tt.slots)
			}
			if st.State != schemas.ExperimentDone && st.FinishedAt != nil {
				t.Errorf("finished_at %v with state %s", st.FinishedAt, st.State)
			}
		})
	}
	t.Run("analysis id passes through", func(t *testing.T) {
		if st := BuildStatus(m, runs, StatusFacts{AnalysisID: "a_1"}); st.AnalysisID != "a_1" {
			t.Fatalf("analysis_id = %q", st.AnalysisID)
		}
	})
}

func TestSpentUSD(t *testing.T) {
	sealed := func(id string, outcome schemas.Outcome) RunView {
		return RunView{Sealed: true, Run: schemas.Run{RunID: id, Outcome: outcome}}
	}
	obs := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }
	tests := []struct {
		name     string
		runs     []RunView
		costs    map[string]schemas.Value
		state    schemas.ValueState
		v        float64
		bounds   *[2]float64
		reason   string
		observed bool
	}{
		{name: "nothing sealed is zero spent", state: schemas.ValueObserved, observed: true},
		{name: "sum of observed", runs: []RunView{sealed("r1", schemas.OutcomePass), sealed("r2", schemas.OutcomeFail)},
			costs: map[string]schemas.Value{"r1": obs(0.5), "r2": obs(0.25)}, state: schemas.ValueObserved, v: 0.75, observed: true},
		{name: "unsealed runs are left out", runs: []RunView{sealed("r1", schemas.OutcomePass), {Run: schemas.Run{RunID: "r2"}}},
			costs: map[string]schemas.Value{"r1": obs(0.5)}, state: schemas.ValueObserved, v: 0.5, observed: true},
		{name: "aborted before the agent started spent nothing", runs: []RunView{sealed("r1", schemas.OutcomeAbortedInfra), sealed("r2", schemas.OutcomePass)},
			costs: map[string]schemas.Value{"r2": obs(1)}, state: schemas.ValueObserved, v: 1, observed: true},
		{name: "missing metrics is unknown", runs: []RunView{sealed("r1", schemas.OutcomePass), sealed("r2", schemas.OutcomeCrash)},
			costs: map[string]schemas.Value{"r1": obs(1)}, state: schemas.ValueUnknown, reason: "no_metrics"},
		{name: "bounded unknowns keep bounds", runs: []RunView{sealed("r1", schemas.OutcomePass), sealed("r2", schemas.OutcomePass)},
			costs: map[string]schemas.Value{"r1": obs(1), "r2": schemas.UnknownWithin("telemetry_incomplete", schemas.EvidenceGateway, 0.1, 0.4)},
			state: schemas.ValueUnknown, bounds: &[2]float64{1.1, 1.4}, reason: "telemetry_incomplete"},
		{name: "not applicable is left out", runs: []RunView{sealed("r1", schemas.OutcomePass)},
			costs: map[string]schemas.Value{"r1": schemas.NotApplicable()}, state: schemas.ValueObserved, observed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := spentUSD(tt.runs, tt.costs)
			if got.State != tt.state || got.Reason != tt.reason {
				t.Fatalf("spent = %+v", got)
			}
			if v, ok := got.Float(); ok != tt.observed || (ok && v != tt.v) {
				t.Errorf("v = %v %v, want %v", v, ok, tt.v)
			}
			if (got.Bounds == nil) != (tt.bounds == nil) || (got.Bounds != nil && *got.Bounds != *tt.bounds) {
				t.Errorf("bounds = %v, want %v", got.Bounds, tt.bounds)
			}
		})
	}
}

func TestPlanConditions(t *testing.T) {
	got := planConditions(testManifest())
	want := []schemas.PlanCondition{{Condition: "clean", Tasks: 2, Trials: 1, Slots: 4}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("planConditions = %+v, want %+v", got, want)
	}
}
