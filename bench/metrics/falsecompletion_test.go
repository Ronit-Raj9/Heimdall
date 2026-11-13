package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestFalseCompletion(t *testing.T) {
	outcome, native := schemas.EvidenceOutcome, schemas.EvidenceNative
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "claimed on a failure", ev: &schemas.Evidence{Grade: failed(), Claim: claim(true, native)}, want: schemas.Observed(1, native)},
		{name: "claim read from stdout tail", ev: &schemas.Evidence{Grade: failed(), Claim: claim(true, outcome)}, want: schemas.Observed(1, outcome)},
		{name: "honest failure", ev: &schemas.Evidence{Grade: failed(), Claim: claim(false, native)}, want: schemas.Observed(0, native)},
		{name: "veto ignored, grade decides", ev: &schemas.Evidence{Grade: passed(), Claim: claim(true, native), Gates: []schemas.GateResult{veto(true)}}, want: schemas.NotApplicable()},
		{name: "pass is not applicable", ev: &schemas.Evidence{Grade: passed(), Claim: claim(true, native)}, want: schemas.NotApplicable()},
		{name: "pass without claim is not applicable", ev: &schemas.Evidence{Grade: passed()}, want: schemas.NotApplicable()},
		{name: "failure without claim evidence", ev: &schemas.Evidence{Grade: failed()}, want: schemas.Unknown(reasonNoClaim, outcome)},
		{name: "no grade", ev: &schemas.Evidence{Claim: claim(true, native)}, want: schemas.Unknown(reasonNoGrade, outcome)},
		{name: "invalid evaluator", ev: &schemas.Evidence{Grade: &schemas.Grade{InvalidEvaluator: true}, Claim: claim(true, native)}, want: schemas.Unknown(reasonUndecidable, outcome)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoGrade, outcome)},
	}
	m := newFalseCompletionMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}
