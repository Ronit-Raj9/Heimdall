package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBuildStatus(t *testing.T) {
	idx := testIndex()
	m := idx.manifests[testExp]
	runs := idx.Runs(testExp)
	tests := []struct {
		name  string
		runs  []RunView
		state schemas.ExperimentState
		slots schemas.SlotProgress
	}{
		{"no runs", nil, schemas.ExperimentCreatedState, schemas.SlotProgress{Total: 4}},
		{"all sealed", runs, schemas.ExperimentDone, schemas.SlotProgress{Total: 4, Done: 4}},
		{"one in flight", func() []RunView {
			r := append([]RunView(nil), runs...)
			r[0].Sealed = false
			return r
		}(), schemas.ExperimentRunning, schemas.SlotProgress{Total: 4, Done: 3, Running: 1}},
		{"infra failure", func() []RunView {
			r := append([]RunView(nil), runs...)
			r[1].Run.Outcome = schemas.OutcomeInfraError
			return r
		}(), schemas.ExperimentDone, schemas.SlotProgress{Total: 4, Done: 3, Failed: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := BuildStatus(m, tt.runs)
			if st.State != tt.state || st.Slots != tt.slots {
				t.Fatalf("state %s slots %+v, want %s %+v", st.State, st.Slots, tt.state, tt.slots)
			}
			if st.SpentUSD.IsObserved() {
				t.Errorf("spend must stay unknown, got %+v", st.SpentUSD)
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
