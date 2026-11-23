package verdict

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fidelityGate runs the built-in model_fidelity gate on one run's gateway rows.
func fidelityGate(t *testing.T, agent *schemas.AgentConfig, rows ...schemas.GatewayRequest) schemas.GateResult {
	t.Helper()
	for _, g := range gates.Builtin() {
		if g.GetName() != gates.ModelFidelity {
			continue
		}
		res, err := g.Check(context.Background(), &schemas.RunContext{Agent: agent}, &schemas.Evidence{Requests: &schemas.RequestLog{Requests: rows, Complete: true}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Version != "1.1.0" {
			t.Fatalf("model_fidelity version %s, want 1.1.0", res.Version)
		}
		return *res
	}
	t.Fatal("model_fidelity is not a built-in gate")
	return schemas.GateResult{}
}

// Fix wave 3, N200 with N215 and N244 (group B): a two-setup comparison whose
// failover arm answered through its declared fallback as a dated snapshot of
// the same model (and whose other arm was served the requested alias as its
// dated snapshot) is not voided: model_fidelity 1.1.0 does not fire, so the
// verdict has no invalid reason. A fallback served by a truly different model
// still fires on every run of that arm, and only the runs are untrustworthy
// (scope "runs").
func TestTwoSetupDatedSnapshotFallbackNotVoided(t *testing.T) {
	clean := schemas.ConditionClean
	counts := []int{3, 3, 0, 2, 0, 2, 1, 3}
	agentA := &schemas.AgentConfig{Model: "gpt-5.4-mini"}
	agentB := &schemas.AgentConfig{Model: "gpt-6-luna", Gateway: schemas.GatewayPolicy{Fallbacks: []string{"openai/gpt-5.4-mini"}}}
	build := func(fallbackServed string) []schemas.SealedRun {
		runs := concat(passes(clean, ArmBaseline, 3, counts...), passes(clean, ArmCandidate, 3, counts...))
		for i := range runs {
			var g schemas.GateResult
			if runs[i].Slot.Arm == ArmBaseline {
				g = fidelityGate(t, agentA, schemas.GatewayRequest{ID: "a1", Kind: "message", ModelRequested: "gpt-5.4-mini", ModelServed: "gpt-5.4-mini-2026-03-17"})
			} else {
				g = fidelityGate(t, agentB,
					schemas.GatewayRequest{ID: "b1", Kind: "message", ModelRequested: "gpt-6-luna"},
					schemas.GatewayRequest{ID: "b2", Kind: "message", ModelRequested: "gpt-6-luna", ModelServed: fallbackServed, FallbackIndex: 1})
			}
			runs[i].Gates = append(runs[i].Gates, g)
		}
		return runs
	}
	m := testManifest(ArmBaseline, ArmCandidate)
	runs := build("gpt-5.4-mini-2026-03-17")
	for _, r := range runs {
		if r.Gates[len(r.Gates)-1].Fired {
			t.Fatalf("run %s arm %s: model_fidelity fired on a dated snapshot: %q", r.Run.RunID, r.Slot.Arm, r.Gates[len(r.Gates)-1].Detail)
		}
	}
	an, err := (&pairedSignFlipPolicy{}).Decide(context.Background(), m, runs)
	if err != nil {
		t.Fatal(err)
	}
	if an.Verdict.Decision == schemas.DecisionInvalid || len(an.Verdict.InvalidReasons) != 0 || an.Verdict.InvalidScope != "" {
		t.Fatalf("voided: decision %s scope %q reasons %q", an.Verdict.Decision, an.Verdict.InvalidScope, an.Verdict.InvalidReasons)
	}

	an, err = (&pairedSignFlipPolicy{}).Decide(context.Background(), m, build("claude-haiku-4-5"))
	if err != nil {
		t.Fatal(err)
	}
	if an.Verdict.Decision != schemas.DecisionInvalid || an.Verdict.InvalidScope != "runs" {
		t.Fatalf("other model: decision %s scope %q reasons %q, want invalid in scope runs", an.Verdict.Decision, an.Verdict.InvalidScope, an.Verdict.InvalidReasons)
	}
	for _, r := range an.Verdict.InvalidReasons {
		if !IsGateRateReason(r) {
			t.Fatalf("reason %q is not a gate rate reason", r)
		}
	}
}
