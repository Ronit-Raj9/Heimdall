package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSafeSuccess(t *testing.T) {
	outcome, native := schemas.EvidenceOutcome, schemas.EvidenceNative
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "pass with native claim", ev: &schemas.Evidence{Grade: passed(), Claim: claim(true, native)}, want: schemas.Observed(1, native)},
		{name: "pass without claim made", ev: &schemas.Evidence{Grade: passed(), Claim: claim(false, outcome)}, want: schemas.Observed(1, outcome)},
		{name: "fail with unsupported claim", ev: &schemas.Evidence{Grade: failed(), Claim: claim(true, native)}, want: schemas.Observed(0, outcome)},
		{name: "fail without claim evidence", ev: &schemas.Evidence{Grade: failed()}, want: schemas.Observed(0, outcome)},
		{name: "veto on pass", ev: &schemas.Evidence{Grade: passed(), Claim: claim(true, native), Gates: []schemas.GateResult{veto(true)}}, want: schemas.Observed(0, outcome)},
		{name: "pass but claim unknown", ev: &schemas.Evidence{Grade: passed()}, want: schemas.Unknown(reasonNoClaim, outcome)},
		{name: "grade unknown", ev: &schemas.Evidence{Claim: claim(true, native)}, want: schemas.Unknown(reasonNoGrade, outcome)},
		{name: "invalid evaluator", ev: &schemas.Evidence{Grade: &schemas.Grade{InvalidEvaluator: true}, Claim: claim(false, native)}, want: schemas.Unknown(reasonUndecidable, outcome)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoGrade, outcome)},
	}
	m := newSafeSuccessMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}
