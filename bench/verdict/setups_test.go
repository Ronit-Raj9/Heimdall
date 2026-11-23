package verdict

import (
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestDecideSetupsTiedWhenHolmCannotSeparate is shaped like the live
// comparison e_zz_test_matrix3_6z3h (fix wave 3, N244): three setups on two
// tasks, A and C solve both, B solves t01 and breaks a safety rule on t02.
// The verdict is one story for all three setups: they cannot be told apart,
// and B carries a safety flag, never "B is worse than A".
func TestDecideSetupsTiedWhenHolmCannotSeparate(t *testing.T) {
	m := testManifest(ArmBaseline, ArmCandidate, "C")
	m.Experiment.Conditions[0].Trials = 1
	runs := concat(
		pattern(schemas.ConditionClean, ArmBaseline, "1", "1"),
		withVeto(pattern(schemas.ConditionClean, ArmCandidate, "1", "1"), "t02", ArmCandidate),
		pattern(schemas.ConditionClean, "C", "1", "1"),
	)
	v := decideOrFail(t, m, runs).Verdict
	if v.Decision != schemas.DecisionInconclusive || v.Action != schemas.ActionNoDecision {
		t.Fatalf("decision %s action %s, want inconclusive no_decision", v.Decision, v.Action)
	}
	if v.Setups == nil {
		t.Fatal("no setups verdict on a three-setup comparison")
	}
	if v.Basis != "" {
		t.Fatalf("basis %q: a setups comparison is not decided by a B-against-A effect or guard", v.Basis)
	}
	s := v.Setups
	if s.Separable || len(s.TopGroup) < 2 || !strings.Contains(s.Note, "Cannot separate") {
		t.Fatalf("setups %+v", s)
	}
	if got := s.Safety[ArmCandidate]; !slices.Equal(got, []string{"t02"}) || len(s.Safety) != 1 {
		t.Fatalf("safety %v, want B: [t02] only", s.Safety)
	}
	for _, p := range s.Pairs {
		if p.ArmX != s.Leader {
			t.Fatalf("pair %+v is not against the leader %s", p, s.Leader)
		}
	}
	if len(s.Pairs) != 2 {
		t.Fatalf("pairs %+v, want the leader against the two others", s.Pairs)
	}
}

// setupsLeaderRuns: ten tasks, A solves all, B and C solve none, so A is
// significantly ahead of both after Holm across three pairs.
func setupsLeaderRuns() []schemas.SealedRun {
	return concat(
		pattern(schemas.ConditionClean, ArmBaseline, repeat("1", 10)...),
		pattern(schemas.ConditionClean, ArmCandidate, repeat("0", 10)...),
		pattern(schemas.ConditionClean, "C", repeat("0", 10)...),
	)
}

func TestDecideSetupsLeader(t *testing.T) {
	m := testManifest(ArmBaseline, ArmCandidate, "C")
	m.Experiment.Conditions[0].Trials = 1
	v := decideOrFail(t, m, setupsLeaderRuns()).Verdict
	if v.Decision != schemas.DecisionLeader {
		t.Fatalf("decision %s, want leader (invalid %v)", v.Decision, v.InvalidReasons)
	}
	s := v.Setups
	if s == nil || s.Leader != ArmBaseline || !slices.Equal(s.TopGroup, []string{ArmBaseline}) || !slices.Equal(s.Behind, []string{ArmCandidate, "C"}) || !s.Separable {
		t.Fatalf("setups %+v", s)
	}
	if len(s.Safety) != 0 {
		t.Fatalf("safety %v, want none", s.Safety)
	}
}

// TestLeaderNotReleasable: a leader verdict never ships, whether or not the
// leader broke a safety rule (D12), so release, Promote and CI treat it like
// an inconclusive verdict.
func TestLeaderNotReleasable(t *testing.T) {
	m := testManifest(ArmBaseline, ArmCandidate, "C")
	m.Experiment.Conditions[0].Trials = 1
	clean := decideOrFail(t, m, setupsLeaderRuns()).Verdict
	if clean.Decision != schemas.DecisionLeader || clean.Action == schemas.ActionShip {
		t.Fatalf("clean leader: decision %s action %s, want leader and not ship", clean.Decision, clean.Action)
	}
	flagged := decideOrFail(t, m, withVeto(setupsLeaderRuns(), "t03", ArmBaseline)).Verdict
	if flagged.Action != schemas.ActionHold {
		t.Fatalf("flagged leader: decision %s action %s, want hold", flagged.Decision, flagged.Action)
	}
	if !slices.Equal(flagged.Setups.Safety[ArmBaseline], []string{"t03"}) {
		t.Fatalf("safety %v", flagged.Setups.Safety)
	}
}

// TestDecideSetupsAAArmIsNotASetup: an A/A copy does not make a comparison
// of three setups, so A, B and A2 keep the A/B verdict.
func TestDecideSetupsAAArmIsNotASetup(t *testing.T) {
	m := testManifest(ArmBaseline, ArmCandidate, ArmAA)
	runs := concat(passes(schemas.ConditionClean, ArmBaseline, 3, 3, 3), passes(schemas.ConditionClean, ArmCandidate, 3, 3, 3),
		passes(schemas.ConditionClean, ArmAA, 3, 3, 3))
	v := decideOrFail(t, m, runs).Verdict
	if v.Setups != nil || v.Effect == nil {
		t.Fatalf("setups %+v effect %v: an A/A copy is not a third setup", v.Setups, v.Effect)
	}
}

// TestDecideRegressedBasisGuard: two setups whose effect is not significant
// (p 1.0) but whose integrity guard fails are regressed by the guard, and the
// verdict says so, so no page prints "B is worse by 50 pp".
func TestDecideRegressedBasisGuard(t *testing.T) {
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Conditions[0].Trials = 1
	runs := concat(
		pattern(schemas.ConditionClean, ArmBaseline, "1", "1"),
		withVeto(pattern(schemas.ConditionClean, ArmCandidate, "1", "1"), "t02", ArmCandidate),
	)
	v := decideOrFail(t, m, runs).Verdict
	if v.Decision != schemas.DecisionRegressed || v.Basis != BasisGuard {
		t.Fatalf("decision %s basis %q, want regressed guard", v.Decision, v.Basis)
	}

	// A real, significant drop on every task decides by the effect.
	worse := concat(pattern(schemas.ConditionClean, ArmBaseline, repeat("1", 10)...), pattern(schemas.ConditionClean, ArmCandidate, repeat("0", 10)...))
	v = decideOrFail(t, m, worse).Verdict
	if v.Decision != schemas.DecisionRegressed || v.Basis != BasisEffect {
		t.Fatalf("decision %s basis %q, want regressed effect", v.Decision, v.Basis)
	}

	// An inconclusive verdict has no basis.
	same := concat(pattern(schemas.ConditionClean, ArmBaseline, "1", "0"), pattern(schemas.ConditionClean, ArmCandidate, "1", "0"))
	if v = decideOrFail(t, m, same).Verdict; v.Basis != "" {
		t.Fatalf("decision %s basis %q, want no basis", v.Decision, v.Basis)
	}
}
