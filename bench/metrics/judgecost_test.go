package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestJudgeCost(t *testing.T) {
	gw := schemas.EvidenceGateway
	judged := &schemas.RunContext{Suite: &schemas.Suite{Grading: schemas.GradingJudged}}
	verified := &schemas.RunContext{Suite: &schemas.Suite{}}
	rec := func(v schemas.Value) *schemas.Grade {
		return &schemas.Grade{Mode: gradeModeAnswer, Judged: true, Judge: &schemas.JudgeRecord{Model: "m", USD: v}}
	}
	tests := []struct {
		name string
		rc   *schemas.RunContext
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "judged grade carries its cost", rc: judged, ev: &schemas.Evidence{Grade: rec(schemas.Observed(0.0123, gw))}, want: schemas.Observed(0.0123, gw)},
		{name: "unpriced judge cost stays unknown", rc: judged, ev: &schemas.Evidence{Grade: rec(schemas.Unknown("judge model m is not in the price snapshot", gw))},
			want: schemas.Unknown("judge model m is not in the price snapshot", gw)},
		{name: "no answer to judge cost nothing", rc: judged, ev: &schemas.Evidence{Grade: &schemas.Grade{Mode: gradeModeAnswer, Reason: "no answer"}}, want: schemas.Observed(0, gw)},
		{name: "judged suite without grade is unknown", rc: judged, ev: &schemas.Evidence{}, want: schemas.Unknown(reasonNoGrade, gw)},
		{name: "judged flag without record is unknown", rc: judged, ev: &schemas.Evidence{Grade: &schemas.Grade{Judged: true}}, want: schemas.Unknown("judged grade without a judge record", gw)},
		{name: "verified suite does not apply", rc: verified, ev: &schemas.Evidence{Grade: passed()}, want: schemas.NotApplicable()},
		{name: "no run context does not apply", ev: &schemas.Evidence{Grade: passed()}, want: schemas.NotApplicable()},
		{name: "judged grade outside a judged suite still reports", rc: verified, ev: &schemas.Evidence{Grade: rec(schemas.Observed(0.5, gw))}, want: schemas.Observed(0.5, gw)},
	}
	m := newJudgeCostMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, tt.rc, tt.ev), tt.want)
		})
	}
}

func TestJudgeCostNeverInCostUSD(t *testing.T) {
	ev := &schemas.Evidence{
		Cost:  []schemas.CostRow{{RequestID: "a", USD: schemas.Observed(1, schemas.EvidenceGateway)}},
		Grade: &schemas.Grade{Judged: true, Judge: &schemas.JudgeRecord{USD: schemas.Observed(5, schemas.EvidenceGateway)}},
	}
	checkValue(t, compute(t, newCostUSDMetric(), nil, ev), schemas.Observed(1, schemas.EvidenceGateway))
}
