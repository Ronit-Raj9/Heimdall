package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fix wave 3, N244 and N247 (group B): a debug run and a readiness check are
// never released, even with a ship action, and a leader verdict is never
// released, with or without an override reason.
func TestNeverReleasable(t *testing.T) {
	tests := []struct {
		name     string
		opt      func(*schemas.Manifest)
		leader   bool
		override string
		refused  string
	}{
		{name: "debug run", opt: func(m *schemas.Manifest) { m.Experiment.Debug = &schemas.DebugSpec{Mode: "replay", FromRun: "r_x"} }, refused: "is a debug run"},
		{name: "debug run with override", opt: func(m *schemas.Manifest) { m.Experiment.Debug = &schemas.DebugSpec{Mode: "fork", FromRun: "r_x"} }, override: "ship it", refused: "is a debug run"},
		{name: "readiness check", opt: func(m *schemas.Manifest) { m.Experiment.Name = "admit-zz-test-qa" }, refused: "is a readiness check"},
		{name: "leader", leader: true, refused: "leader found"},
		{name: "leader with override", leader: true, override: "hotfix, risk accepted", refused: "leader found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []func(*schemas.Manifest)
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			f := newFixture(t, schemas.ActionShip, "", opts...)
			if tt.leader {
				a := &schemas.Analysis{AnalysisID: "a_two", ExpID: testExp, CreatedAt: time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC), GradeSource: schemas.GradeSourceOriginal,
					Verdict: schemas.Verdict{Decision: schemas.DecisionLeader, Action: schemas.ActionNoDecision, ManifestHash: f.m.Hash,
						Setups: &schemas.SetupsVerdict{Leader: "B", TopGroup: []string{"B"}, Behind: []string{"A", "C"}, Separable: true}}}
				f.digest(a)
				if err := f.st.WriteAnalysis(context.Background(), a, nil); err != nil {
					t.Fatal(err)
				}
			}
			_, err := f.log.Create(context.Background(), request("ana", tt.override))
			var refused *RefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), tt.refused) {
				t.Fatalf("err = %v, want refusal %q", err, tt.refused)
			}
			if list, lerr := f.log.List(context.Background()); lerr != nil || len(list.Releases) != 0 {
				t.Fatalf("refusal wrote a record: %v %v", list, lerr)
			}
		})
	}
}

// Fix wave 3, N61 (group B): a release record names the rule set by its
// file, as Releases > Rules lists it, not by the name inside the file.
func TestReleaseRecordNamesRuleSetByFile(t *testing.T) {
	f := newFixture(t, schemas.ActionShip, schemas.PolicyAllow)
	a := &schemas.Analysis{AnalysisID: "a_two", ExpID: testExp, CreatedAt: time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC), GradeSource: schemas.GradeSourceOriginal,
		Verdict: schemas.Verdict{Decision: schemas.DecisionImproved, Action: schemas.ActionShip, ManifestHash: f.m.Hash, Effect: &schemas.Effect{ArmA: "A", ArmB: "B"}},
		Policy:  &schemas.PolicyResult{PolicyName: "team", PolicyFile: "strict-release", PolicyHash: "sha256:pol", Decision: schemas.PolicyAllow}}
	f.digest(a)
	if err := f.st.WriteAnalysis(context.Background(), a, nil); err != nil {
		t.Fatal(err)
	}
	rec, err := f.log.Create(context.Background(), request("ana", ""))
	if err != nil {
		t.Fatal(err)
	}
	if rec.PolicyName != "strict-release" {
		t.Fatalf("record policy name %q, want the file name strict-release", rec.PolicyName)
	}
}
