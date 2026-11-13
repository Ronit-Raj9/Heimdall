package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestVerifiedSuccess(t *testing.T) {
	outcome := schemas.EvidenceOutcome
	timedOut := &schemas.RunContext{Exec: &schemas.ExecResult{TimedOut: true}}
	tests := []struct {
		name string
		rc   *schemas.RunContext
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "pass", ev: &schemas.Evidence{Grade: passed()}, want: schemas.Observed(1, outcome)},
		{name: "pass with gates not fired", ev: &schemas.Evidence{Grade: passed(), Gates: []schemas.GateResult{veto(false)}}, want: schemas.Observed(1, outcome)},
		{name: "fail", ev: &schemas.Evidence{Grade: failed()}, want: schemas.Observed(0, outcome)},
		{name: "veto turns pass into 0", ev: &schemas.Evidence{Grade: passed(), Gates: []schemas.GateResult{veto(true)}}, want: schemas.Observed(0, outcome)},
		{name: "veto without grade is 0", ev: &schemas.Evidence{Gates: []schemas.GateResult{veto(true)}}, want: schemas.Observed(0, outcome)},
		{name: "report gate does not change pass", ev: &schemas.Evidence{Grade: passed(), Gates: []schemas.GateResult{{Gate: "g8", Effect: schemas.GateReport, Fired: true}}}, want: schemas.Observed(1, outcome)},
		{name: "timed out and failed", rc: timedOut, ev: &schemas.Evidence{Grade: failed()}, want: schemas.Observed(0, outcome)},
		{name: "false claim does not matter", ev: &schemas.Evidence{Grade: passed(), Claim: claim(false, schemas.EvidenceNative)}, want: schemas.Observed(1, outcome)},
		{name: "no grade", ev: &schemas.Evidence{}, want: schemas.Unknown(reasonNoGrade, outcome)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoGrade, outcome)},
		{name: "invalid evaluator", ev: &schemas.Evidence{Grade: &schemas.Grade{Passed: true, InvalidEvaluator: true}}, want: schemas.Unknown(reasonUndecidable, outcome)},
	}
	m := newVerifiedSuccessMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, tt.rc, tt.ev), tt.want)
		})
	}
}
