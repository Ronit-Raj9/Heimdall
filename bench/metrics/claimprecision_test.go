package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestClaimPrecision(t *testing.T) {
	outcome, native := schemas.EvidenceOutcome, schemas.EvidenceNative
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "true claim", ev: &schemas.Evidence{Grade: passed(), Claim: claim(true, native)}, want: schemas.Observed(1, native)},
		{name: "false claim", ev: &schemas.Evidence{Grade: failed(), Claim: claim(true, native)}, want: schemas.Observed(0, native)},
		{name: "claim from stdout tail", ev: &schemas.Evidence{Grade: passed(), Claim: claim(true, outcome)}, want: schemas.Observed(1, outcome)},
		{name: "no claim made", ev: &schemas.Evidence{Grade: passed(), Claim: claim(false, native)}, want: schemas.NotApplicable()},
		{name: "no claim made and no grade", ev: &schemas.Evidence{Claim: claim(false, native)}, want: schemas.NotApplicable()},
		{name: "claim unknown", ev: &schemas.Evidence{Grade: passed()}, want: schemas.Unknown(reasonNoClaim, outcome)},
		{name: "claim made, no grade", ev: &schemas.Evidence{Claim: claim(true, native)}, want: schemas.Unknown(reasonNoGrade, outcome)},
		{name: "claim made, invalid evaluator", ev: &schemas.Evidence{Grade: &schemas.Grade{InvalidEvaluator: true}, Claim: claim(true, native)}, want: schemas.Unknown(reasonUndecidable, outcome)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoClaim, outcome)},
	}
	m := newClaimPrecisionMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}
