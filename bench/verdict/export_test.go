package verdict

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestReleaseEffectDecision(t *testing.T) {
	tests := []struct {
		name string
		a, b []int
		want schemas.Decision
	}{
		{"regressed on 6 tasks", []int{3, 3, 3, 3, 3, 3}, []int{0, 0, 0, 0, 0, 0}, schemas.DecisionRegressed},
		{"improved on 6 tasks", []int{0, 0, 0, 0, 0, 0}, []int{3, 3, 3, 3, 3, 3}, schemas.DecisionImproved},
		{"worked example inconclusive", []int{3, 3, 0, 2, 0, 2, 1, 3}, []int{3, 3, 1, 3, 0, 3, 2, 3}, schemas.DecisionInconclusive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs := concat(passes(schemas.ConditionClean, ArmBaseline, 3, tt.a...), passes(schemas.ConditionClean, ArmCandidate, 3, tt.b...))
			e := ReleaseEffect(runs, schemas.ConditionClean, MetricVerifiedSuccess, ArmBaseline, ArmCandidate)
			if e.LowerBound == nil || e.UpperBound == nil || e.PValue == nil {
				t.Fatalf("effect lacks bounds or p-value: %+v", e)
			}
			if got := EffectDecision(e, 0.05); got != tt.want {
				t.Fatalf("decision = %s, want %s (d_bar %.3f, p %.4f)", got, tt.want, e.DBar, *e.PValue)
			}
			wantDir := map[schemas.Decision]int{schemas.DecisionRegressed: -1, schemas.DecisionImproved: 1}[tt.want]
			if lo, hi := Direction(e.LowerBound, 0.05), Direction(e.UpperBound, 0.05); lo != wantDir || hi != wantDir {
				t.Fatalf("directions = %d, %d, want %d", lo, hi, wantDir)
			}
		})
	}
}

func TestEffectDecisionNil(t *testing.T) {
	if got := EffectDecision(nil, 0.05); got != schemas.DecisionInconclusive {
		t.Fatalf("nil effect = %s", got)
	}
	if got := Direction(nil, 0.05); got != 0 {
		t.Fatalf("nil direction = %d", got)
	}
}

func TestVetoed(t *testing.T) {
	runs := withVeto(passes(schemas.ConditionClean, ArmCandidate, 1, 1, 1), taskID(0), ArmCandidate)
	if !Vetoed(runs[0]) || Vetoed(runs[1]) {
		t.Fatalf("vetoed = %v, %v", Vetoed(runs[0]), Vetoed(runs[1]))
	}
}
