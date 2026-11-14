package metrics

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func rewardGrade(passed bool, reward *float64, dims map[string]float64) *schemas.Grade {
	return &schemas.Grade{Passed: passed, ResultFormat: schemas.VerifierResultReward, Reward: reward, Dimensions: dims}
}

func num(v float64) *float64 { return &v }

func TestReward(t *testing.T) {
	outcome := schemas.EvidenceOutcome
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "full reward", ev: &schemas.Evidence{Grade: rewardGrade(true, num(1), nil)}, want: schemas.Observed(1, outcome)},
		{name: "partial reward that failed the threshold", ev: &schemas.Evidence{Grade: rewardGrade(false, num(0.25), nil)}, want: schemas.Observed(0.25, outcome)},
		{name: "zero reward is observed", ev: &schemas.Evidence{Grade: rewardGrade(false, num(0), nil)}, want: schemas.Observed(0, outcome)},
		{name: "veto does not change the reward", ev: &schemas.Evidence{Grade: rewardGrade(true, num(1), nil), Gates: []schemas.GateResult{veto(true)}}, want: schemas.Observed(1, outcome)},
		{name: "reward run with no number is unknown", ev: &schemas.Evidence{Grade: &schemas.Grade{ResultFormat: schemas.VerifierResultReward, Reason: "test run broken by the patch: no reward file (exit 0)"}},
			want: schemas.Unknown("verifier wrote no usable reward: test run broken by the patch: no reward file", outcome)},
		{name: "non-finite reward is unknown", ev: &schemas.Evidence{Grade: rewardGrade(false, num(math.Inf(1)), nil)}, want: schemas.Unknown(reasonNoReward, outcome)},
		{name: "junit grade does not apply", ev: &schemas.Evidence{Grade: &schemas.Grade{Passed: true, ResultFormat: schemas.VerifierResultJUnit}}, want: schemas.NotApplicable()},
		{name: "exit code grade does not apply", ev: &schemas.Evidence{Grade: &schemas.Grade{Passed: true, ResultFormat: schemas.VerifierResultExitCode}}, want: schemas.NotApplicable()},
		{name: "hidden_tests grade does not apply", ev: &schemas.Evidence{Grade: passed()}, want: schemas.NotApplicable()},
		{name: "no grade", ev: &schemas.Evidence{}, want: schemas.Unknown(reasonNoGrade, outcome)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown(reasonNoGrade, outcome)},
		{name: "invalid evaluator", ev: &schemas.Evidence{Grade: &schemas.Grade{InvalidEvaluator: true, ResultFormat: schemas.VerifierResultReward, Reward: num(1)}}, want: schemas.Unknown(reasonUndecidable, outcome)},
	}
	m := newRewardMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}

func TestRewardDimensions(t *testing.T) {
	outcome := schemas.EvidenceOutcome
	many := map[string]float64{}
	for i := range maxDimensions + 1 {
		many[fmt.Sprintf("d%d", i)] = 1
	}
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want map[string]schemas.Value
	}{
		{name: "each dimension becomes reward_<name>", ev: &schemas.Evidence{Grade: rewardGrade(false, num(0.5), map[string]float64{"correctness": 0.5, "safety": 1})},
			want: map[string]schemas.Value{"reward_correctness": schemas.Observed(0.5, outcome), "reward_safety": schemas.Observed(1, outcome)}},
		{name: "zero dimension is observed", ev: &schemas.Evidence{Grade: rewardGrade(false, num(0), map[string]float64{"style": 0})},
			want: map[string]schemas.Value{"reward_style": schemas.Observed(0, outcome)}},
		{name: "bad names and non-finite values are dropped", ev: &schemas.Evidence{Grade: rewardGrade(true, num(1), map[string]float64{"Bad-Name": 1, strings.Repeat("a", 41): 1, "nan": math.NaN(), "ok": 1})},
			want: map[string]schemas.Value{"reward_ok": schemas.Observed(1, outcome)}},
		{name: "too many dimensions report none", ev: &schemas.Evidence{Grade: rewardGrade(true, num(1), many)}},
		{name: "no dimensions", ev: &schemas.Evidence{Grade: rewardGrade(true, num(1), nil)}},
		{name: "no reward means no dimensions", ev: &schemas.Evidence{Grade: &schemas.Grade{ResultFormat: schemas.VerifierResultReward, Dimensions: map[string]float64{"a": 1}}}},
		{name: "other formats have none", ev: &schemas.Evidence{Grade: &schemas.Grade{Passed: true, ResultFormat: schemas.VerifierResultJUnit, Dimensions: map[string]float64{"a": 1}}}},
		{name: "invalid evaluator has none", ev: &schemas.Evidence{Grade: &schemas.Grade{InvalidEvaluator: true, ResultFormat: schemas.VerifierResultReward, Reward: num(1), Dimensions: map[string]float64{"a": 1}}}},
		{name: "nil evidence", ev: nil},
	}
	m := newRewardMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.ComputeDerived(context.Background(), nil, tt.ev)
			if err != nil {
				t.Fatalf("ComputeDerived: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, want := range tt.want {
				v, ok := got[k]
				if !ok {
					t.Fatalf("missing %s in %v", k, got)
				}
				checkValue(t, v, want)
			}
			for k := range got {
				if !strings.HasPrefix(k, RewardDimensionPrefix) {
					t.Errorf("derived name %q lacks the %q prefix", k, RewardDimensionPrefix)
				}
			}
		})
	}
}

func TestRewardDoesNotTouchVerifiedSuccess(t *testing.T) {
	tests := []struct {
		name  string
		grade *schemas.Grade
		want  float64
	}{
		{name: "high reward below threshold still fails", grade: rewardGrade(false, num(0.99), map[string]float64{"correctness": 1}), want: 0},
		{name: "pass rule alone decides", grade: rewardGrade(true, num(1), map[string]float64{"correctness": 0}), want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compute(t, newVerifiedSuccessMetric(), nil, &schemas.Evidence{Grade: tt.grade})
			if !reflect.DeepEqual(got, schemas.Observed(tt.want, schemas.EvidenceOutcome)) {
				t.Fatalf("verified_success = %+v, want %v", got, tt.want)
			}
		})
	}
}
