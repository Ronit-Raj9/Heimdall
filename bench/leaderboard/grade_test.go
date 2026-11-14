package leaderboard

import (
	"context"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestExcludeReason(t *testing.T) {
	live, invalid := &expInfo{id: "e"}, &expInfo{id: "e", invalid: true}
	tests := []struct {
		name string
		rec  record
		want string
	}{
		{name: "pass", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomePass}}, want: ""},
		{name: "fail", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeFail}}, want: ""},
		{name: "integrity violation is graded", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeIntegrityViolation}}, want: ""},
		{name: "timeout is graded", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeTimeout}}, want: ""},
		{name: "infra error", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeInfraError}}, want: "infra_error"},
		{name: "invalid evaluator", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeInvalidEvaluator}}, want: "invalid_evaluator"},
		{name: "aborted", rec: record{exp: live, sealed: true, run: schemas.Run{Outcome: schemas.OutcomeAbortedInfra}}, want: "aborted_infra"},
		{name: "no outcome", rec: record{exp: live, sealed: true}, want: "no outcome"},
		{name: "unsealed", rec: record{exp: live, run: schemas.Run{Outcome: schemas.OutcomePass}}, want: ReasonNotSealed},
		{name: "invalid verdict wins", rec: record{exp: invalid, run: schemas.Run{Outcome: schemas.OutcomePass}}, want: ReasonVerdictInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := excludeReason(&tt.rec); got != tt.want {
				t.Errorf("excludeReason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGradeSuccess(t *testing.T) {
	f := newFake()
	f.exp("e", "s@1", 0, map[string]schemas.ArmManifest{"A": arm("a", "h", "command")})
	f.run("e", "t1", "A", "clean", 1, 1, schemas.OutcomePass, true, metricsJSON(1, 0.1))
	// A veto after a passing verifier: integrity_violation, verified_success 0.
	f.run("e", "t2", "A", "clean", 1, 1, schemas.OutcomeIntegrityViolation, true, metricsJSON(0, 0.1))
	// Measured before verified_success existed: the outcome decides.
	f.run("e", "t3", "A", "clean", 1, 1, schemas.OutcomePass, true, "")
	f.run("e", "t4", "A", "clean", 1, 1, schemas.OutcomeTimeout, true, `{"verified_success":{"state":"unknown","v":null,"reason":"no grade"}}`)
	recs, err := collect(context.Background(), f)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	p, excluded, err := grade(context.Background(), f, recs)
	if err != nil {
		t.Fatalf("grade: %v", err)
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %+v, want none", excluded)
	}
	got := map[string]float64{}
	for _, r := range p["h"]["clean"] {
		got[r.rec.slot.Task] = r.success
	}
	want := map[string]float64{"t1": 1, "t2": 0, "t3": 1, "t4": 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("success = %v, want %v", got, want)
	}
}

func TestExclusions(t *testing.T) {
	older := &expInfo{id: "e_b", m: &schemas.Manifest{CreatedAt: t0}}
	newer := &expInfo{id: "e_a", m: &schemas.Manifest{CreatedAt: t0.Add(1)}}
	rec := func(e *expInfo, cond string) *record { return &record{exp: e, slot: schemas.Slot{Condition: cond}} }
	got := exclusions([]excludedRun{
		{rec: rec(older, "clean"), reason: "not sealed"},
		{rec: rec(newer, "clean"), reason: "not sealed"},
		{rec: rec(newer, "clean"), reason: "infra_error"},
		{rec: rec(newer, "model-outage"), reason: "infra_error"},
	}, "clean")
	want := []schemas.LeaderboardExclusion{
		{Reason: "infra_error", Runs: 1, Experiments: []string{"e_a"}},
		{Reason: "not sealed", Runs: 2, Experiments: []string{"e_a", "e_b"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exclusions = %+v, want %+v", got, want)
	}
}
