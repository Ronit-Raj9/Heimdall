package metrics

import (
	"context"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func answerGrade(passed bool, category string) *schemas.Grade {
	return &schemas.Grade{Passed: passed, Mode: gradeModeAnswer, Category: category}
}

func answerTaskRC() *schemas.RunContext {
	return &schemas.RunContext{Suite: &schemas.Suite{Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}}, Task: &schemas.Task{ID: "q1"}}
}

func TestAnswerCorrect(t *testing.T) {
	outcome := schemas.EvidenceOutcome
	tests := []struct {
		name string
		rc   *schemas.RunContext
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "correct answer", ev: &schemas.Evidence{Grade: answerGrade(true, "temporal-reasoning")}, want: schemas.Observed(1, outcome)},
		{name: "wrong answer", ev: &schemas.Evidence{Grade: answerGrade(false, "")}, want: schemas.Observed(0, outcome)},
		{name: "veto scores 0", ev: &schemas.Evidence{Grade: answerGrade(true, "x"), Gates: []schemas.GateResult{veto(true)}}, want: schemas.Observed(0, outcome)},
		{name: "undecided is unknown", ev: &schemas.Evidence{Grade: &schemas.Grade{Mode: gradeModeAnswer, InvalidEvaluator: true}}, want: schemas.Unknown(reasonUndecidable, outcome)},
		{name: "answer task without grade is unknown", rc: answerTaskRC(), ev: &schemas.Evidence{}, want: schemas.Unknown(reasonNoGrade, outcome)},
		{name: "diff task does not apply", ev: &schemas.Evidence{Grade: passed()}, want: schemas.NotApplicable()},
		{name: "no grade no task does not apply", ev: nil, want: schemas.NotApplicable()},
	}
	m := newAnswerCorrectMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, tt.rc, tt.ev), tt.want)
		})
	}
}

func TestAnswerCorrectDerived(t *testing.T) {
	outcome := schemas.EvidenceOutcome
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want map[string]schemas.Value
	}{
		{name: "category becomes answer_<name>", ev: &schemas.Evidence{Grade: answerGrade(true, "temporal-reasoning")},
			want: map[string]schemas.Value{"answer_temporal_reasoning": schemas.Observed(1, outcome)}},
		{name: "wrong answer is 0 in its category", ev: &schemas.Evidence{Grade: answerGrade(false, "Multi Hop!")},
			want: map[string]schemas.Value{"answer_multi_hop": schemas.Observed(0, outcome)}},
		{name: "no category", ev: &schemas.Evidence{Grade: answerGrade(true, "")}},
		{name: "category without usable characters", ev: &schemas.Evidence{Grade: answerGrade(true, "--")}},
		{name: "undecided grade gives none", ev: &schemas.Evidence{Grade: &schemas.Grade{Mode: gradeModeAnswer, InvalidEvaluator: true, Category: "x"}}},
		{name: "no grade", ev: &schemas.Evidence{}},
		{name: "diff grade gives none", ev: &schemas.Evidence{Grade: &schemas.Grade{Passed: true, Category: "x"}}},
	}
	m := newAnswerCorrectMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.ComputeDerived(context.Background(), nil, tt.ev)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if !reflect.DeepEqual(got[k], v) {
					t.Fatalf("%s = %+v, want %+v", k, got[k], v)
				}
			}
		})
	}
}

func TestCategoryName(t *testing.T) {
	tests := map[string]string{
		"single-session-user": "single_session_user",
		"  Open Domain ":      "open_domain",
		"a__b":                "a_b",
		"":                    "",
		"abcdefghij-abcdefghij-abcdefghij-abcdefghij":  "abcdefghij_abcdefghij_abcdefghij_abcdefg",
		"abcdefghij-abcdefghij-abcdefghij-abcdefg-hij": "abcdefghij_abcdefghij_abcdefghij_abcdefg",
	}
	for in, want := range tests {
		if got := categoryName(in); got != want {
			t.Errorf("categoryName(%q) = %q, want %q", in, got, want)
		}
	}
}
