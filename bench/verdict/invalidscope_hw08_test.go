package verdict

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fix wave 3, N215: an experiment made invalid only by an invalidate gate's
// per-arm rate is invalid in scope "runs" (only the runs the gate fired on
// are untrustworthy); any other reason makes the whole experiment invalid.
func TestInvalidScopeRunsWhenOnlyGateRate(t *testing.T) {
	clean := schemas.ConditionClean
	counts := []int{3, 3, 0, 2, 0, 2, 1, 3}
	base := concat(passes(clean, ArmBaseline, 3, counts...), passes(clean, ArmCandidate, 3, counts...))
	fired := schemas.GateResult{Gate: "model_fidelity", Effect: schemas.GateInvalidate, Fired: true}
	runs := withGate(base, ArmCandidate, 7, fired)
	m := testManifest(ArmBaseline, ArmCandidate)
	cfg := settingsFor(m)
	v := verdictFor(m, cfg, newRunIndex(runs), runs, runs, false)
	if v.Decision != schemas.DecisionInvalid {
		t.Fatalf("decision %s, want invalid (reasons %q)", v.Decision, v.InvalidReasons)
	}
	if v.InvalidScope != "runs" {
		t.Fatalf("invalid_scope %q, want runs (reasons %q)", v.InvalidScope, v.InvalidReasons)
	}
	ok := verdictFor(m, cfg, newRunIndex(base), base, base, false)
	if ok.InvalidScope != "" {
		t.Fatalf("a decision that is not invalid has no scope, got %q", ok.InvalidScope)
	}
}

func TestInvalidScopeExperimentOnCoverage(t *testing.T) {
	clean := schemas.ConditionClean
	gapB := pattern(clean, ArmCandidate, "1uu", "111", "111", "111", "111", "111", "111", "111", "111", "111")
	gapA := passes(clean, ArmBaseline, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3)
	fired := schemas.GateResult{Gate: "model_fidelity", Effect: schemas.GateInvalidate, Fired: true}
	for name, runs := range map[string][]schemas.SealedRun{
		"coverage only":          concat(gapA, gapB),
		"coverage and gate rate": withGate(concat(gapA, gapB), ArmCandidate, 10, fired),
	} {
		m := testManifest(ArmBaseline, ArmCandidate)
		v := verdictFor(m, settingsFor(m), newRunIndex(runs), runs, runs, false)
		if v.Decision != schemas.DecisionInvalid || v.InvalidScope != "experiment" {
			t.Fatalf("%s: decision %s scope %q, want invalid experiment (reasons %q)", name, v.Decision, v.InvalidScope, v.InvalidReasons)
		}
	}
}

// A stored "runs" scope is trusted only while every reason is a gate rate
// reason; IsGateRateReason tells them apart.
func TestIsGateRateReason(t *testing.T) {
	clean := schemas.ConditionClean
	base := concat(passes(clean, ArmBaseline, 3, 3, 3, 0, 2), passes(clean, ArmCandidate, 3, 3, 3, 0, 2))
	runs := withGate(base, ArmCandidate, 7, schemas.GateResult{Gate: "model_fidelity", Effect: schemas.GateInvalidate, Fired: true})
	got := invalidateGateReasons(newRunIndex(runs))
	if len(got) == 0 || !IsGateRateReason(got[0]) {
		t.Fatalf("gate rate reasons %q", got)
	}
	for _, r := range []string{"seals.log chain does not verify", "graded coverage 0.500 (6 of 12 runs) in arm A on clean is below 0.90", "egress gate fired in 1 run(s): r1"} {
		if IsGateRateReason(r) {
			t.Fatalf("%q read as a gate rate reason", r)
		}
	}
}
