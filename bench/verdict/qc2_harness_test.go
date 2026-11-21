package verdict

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func egressFired(r schemas.SealedRun) schemas.SealedRun {
	r.Gates = append(slices.Clip(r.Gates), schemas.GateResult{Gate: GateEgress, Effect: schemas.GateInvalidate, Fired: true})
	return r
}

func hasReason(reasons []string, sub string) bool {
	return slices.ContainsFunc(reasons, func(s string) bool { return strings.Contains(s, sub) })
}

// Audit qc2-03-07: a run the adaptive rule leaves out of the statistics
// still has its egress result read.
func TestAdaptiveExcludedRunStillChecksEgress(t *testing.T) {
	m := w7Manifest(schemas.AdaptiveTask, 8, 2, 4, 0, "")
	outcome := func(sl schemas.Slot) (float64, bool) {
		if sl.Task == "t07" || sl.Task == "t08" {
			return 1, sl.Trial <= 2
		}
		if sl.Arm == ArmCandidate {
			return 1, true
		}
		return 0, true
	}
	runs := runsFor(m, outcome)
	extra := append(slices.Clip(runs), egressFired(sealedRun("t07", ArmBaseline, schemas.ConditionClean, 3, schemas.Observed(1, schemas.EvidenceOutcome))),
		sealedRun("t07", ArmCandidate, schemas.ConditionClean, 3, schemas.Observed(1, schemas.EvidenceOutcome)))
	a, err := newPairedSignFlipPolicy().Decide(context.Background(), m, extra)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Verdict.Adaptive.Excluded) != 2 {
		t.Fatalf("excluded %v, want the two t07 trial 3 runs", a.Verdict.Adaptive.Excluded)
	}
	if a.Verdict.Decision != schemas.DecisionInvalid || !hasReason(a.Verdict.InvalidReasons, "egress gate fired") {
		t.Fatalf("decision %s reasons %v, want INVALID for egress", a.Verdict.Decision, a.Verdict.InvalidReasons)
	}
}

// Audit qc2-02-05: an uncounted attempt (a superseded retry, as
// store.HarnessAttempts hands it over) feeds the egress check but neither
// fills its slot nor enters the paired arithmetic.
func TestUncountedAttemptFeedsEgressOnly(t *testing.T) {
	m := w7Manifest(schemas.AdaptiveTask, 8, 4, 4, 0, "")
	m.Adaptive = nil
	outcome := func(sl schemas.Slot) (float64, bool) {
		if sl.Arm == ArmCandidate {
			return 1, true
		}
		return 0, true
	}
	runs := runsFor(m, outcome)
	base, err := newPairedSignFlipPolicy().Decide(context.Background(), m, runs)
	if err != nil {
		t.Fatal(err)
	}
	if base.Verdict.Decision != schemas.DecisionImproved {
		t.Fatalf("baseline decision %s %v", base.Verdict.Decision, base.Verdict.InvalidReasons)
	}
	// The superseded attempt of a slot whose counted run is missing.
	missing := runs[0]
	rest := runs[1:]
	stale := egressFired(missing)
	stale.Slot.Counted = false
	stale.Run.RunID += "_attempt1"
	a, err := newPairedSignFlipPolicy().Decide(context.Background(), m, append(slices.Clip(rest), stale))
	if err != nil {
		t.Fatal(err)
	}
	if !hasReason(a.Verdict.InvalidReasons, "egress gate fired") {
		t.Fatalf("reasons %v, want the superseded attempt's egress", a.Verdict.InvalidReasons)
	}
	if c := checkNamed(a.Verdict.Instruments, InstrumentSlotCoverage); c == nil || c.Status != schemas.InstrumentWarn {
		t.Fatalf("slot coverage %+v: the uncounted attempt must not fill its slot", c)
	}
}

// Audit qc2-02-06: the release policy's population is the verdict's.
func TestDecisionRunsDropsExcludedRuns(t *testing.T) {
	m := w7Manifest(schemas.AdaptiveTask, 8, 2, 4, 0, "")
	outcome := func(sl schemas.Slot) (float64, bool) {
		if sl.Task == "t07" || sl.Task == "t08" {
			return 1, sl.Trial <= 2
		}
		if sl.Arm == ArmCandidate {
			return 1, true
		}
		return 0, true
	}
	runs := runsFor(m, outcome)
	x := sealedRun("t07", ArmBaseline, schemas.ConditionClean, 3, schemas.Observed(1, schemas.EvidenceOutcome))
	got := DecisionRuns(m, append(slices.Clip(runs), x))
	if slices.ContainsFunc(got, func(r schemas.SealedRun) bool { return r.Run.RunID == x.Run.RunID }) {
		t.Fatal("DecisionRuns kept a run the adaptive rule left out")
	}
	if len(got) != len(CompleteSlots(m, AdaptiveRuns(m, runs))) {
		t.Fatalf("population %d", len(got))
	}
}

// Audit qc2-02-02: an executed-tool count known only from the agent's native
// stream, or mixed with native events, neither decides nor counts as graded.
func TestAgentStreamValuesCannotDecide(t *testing.T) {
	clean := schemas.ConditionClean
	for _, tc := range []struct {
		name string
		val  func(x float64) schemas.Value
	}{
		{"agent_stream", func(x float64) schemas.Value {
			v := schemas.Observed(x, schemas.EvidenceNative)
			v.Source, v.EvidenceLevel = schemas.EvidenceSourceAgentStream, schemas.EvidencePartial
			return v
		}},
		{"mixed tool_runner", func(x float64) schemas.Value {
			v := schemas.Observed(x, schemas.EvidenceBoundary)
			v.Source, v.EvidenceLevel = schemas.EvidenceSourceToolRunner, schemas.EvidencePartial
			return v
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := passes(clean, ArmBaseline, 1, 1, 1, 1, 1, 1, 1, 1, 1)
			b := passes(clean, ArmCandidate, 1, 1, 1, 1, 1, 1, 1, 1, 1)
			var av, bv []schemas.Value
			for range a {
				av, bv = append(av, tc.val(2)), append(bv, tc.val(1))
			}
			a, b = withMetric(a, "tool_calls_executed", av...), withMetric(b, "tool_calls_executed", bv...)
			m := testManifest(ArmBaseline, ArmCandidate)
			m.Experiment.Conditions = []schemas.ConditionSpec{{ID: clean, Trials: 1}}
			m.Experiment.Primary.Metric = "tool_calls_executed"
			an := decideOrFail(t, m, concat(a, b))
			if d := an.Verdict.Decision; d == schemas.DecisionImproved || d == schemas.DecisionRegressed {
				t.Fatalf("unwitnessed metric decided %s", d)
			}
			if !hasReason(an.Verdict.InvalidReasons, "graded coverage") {
				t.Fatalf("reasons %v, want unwitnessed values to miss graded coverage", an.Verdict.InvalidReasons)
			}
		})
	}
}

// Audit qc2-02-03: unrun slot placeholders do not dilute an invalidate
// gate's failure rate.
func TestInvalidateRateIgnoresUnrunSlots(t *testing.T) {
	m := w7Manifest(schemas.AdaptiveTask, 32, 1, 1, 0, "")
	m.Adaptive = nil
	runs := runsFor(m, func(sl schemas.Slot) (float64, bool) { return 1, true })
	var kept []schemas.SealedRun
	for _, r := range runs {
		// Only the first eight tasks ran; the other 24 per arm never did.
		if r.Slot.Task <= taskID(7) {
			kept = append(kept, r)
		}
	}
	for i := range kept {
		if kept[i].Slot.Arm == ArmCandidate {
			kept[i].Gates = []schemas.GateResult{{Gate: "model_fidelity", Effect: schemas.GateInvalidate, Fired: true}}
			break
		}
	}
	got := invalidateGateReasons(newRunIndex(CompleteSlots(m, kept)))
	if len(got) != 1 || !strings.Contains(got[0], "1 of 8 runs") {
		t.Fatalf("reasons %v, want model_fidelity at 1 of 8 runs", got)
	}
}

// Audit qc2-02-04: the A/A check needs coverage of clean arm A too, not only
// of A2: A observed on one of three clean tasks leaves it unknown.
func TestAACoverageChecksArmA(t *testing.T) {
	clean := schemas.ConditionClean
	m := testManifest(ArmBaseline, ArmCandidate, ArmAA)
	m.Experiment.Primary = schemas.Endpoint{Condition: condStale, Metric: MetricVerifiedSuccess}
	m.Experiment.Guards = []schemas.GuardSpec{{Rule: RuleNoNewIntegrityViolation}}
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: clean, Trials: 1}, {ID: condStale, Trials: 1}}
	a := passes(clean, ArmBaseline, 1, 1, 1)
	for i := 1; i < len(a); i++ {
		a[i].Metrics = map[string]schemas.Value{MetricVerifiedSuccess: schemas.Unknown("grader crashed", schemas.EvidenceOutcome)}
	}
	runs := concat(
		passes(condStale, ArmBaseline, 1, 0, 0, 0, 0, 0, 0, 0, 0),
		passes(condStale, ArmCandidate, 1, 1, 1, 1, 1, 1, 1, 1, 1),
		a,
		passes(clean, ArmCandidate, 1, 1, 1),
		passes(clean, ArmAA, 1, 1, 1),
	)
	an := decideOrFail(t, withSlots(m, runs), runs)
	check := checkNamed(an.Verdict.Instruments, InstrumentAANoiseFloor)
	if check == nil || check.Status != schemas.InstrumentUnknown || !strings.Contains(check.Detail, "arm "+ArmBaseline+" has") {
		t.Fatalf("aa_noise_floor %+v, want unknown naming arm A", check)
	}
	if an.Verdict.Action == schemas.ActionShip {
		t.Fatalf("shipped with most clean A observations missing: %s/%s", an.Verdict.Decision, an.Verdict.Action)
	}
}

// Audit qc2-03-03: a stage held by a failed hold-only guard withholds a
// combined IMPROVED; a REGRESSED combination stands.
func TestStageHoldWithholdsCombinedImprovement(t *testing.T) {
	held := &schemas.Analysis{ExpID: "e_child", Verdict: schemas.Verdict{Decision: schemas.DecisionInconclusive, Action: schemas.ActionHold,
		Guards: []schemas.GuardResult{{Spec: schemas.GuardSpec{Rule: RuleTimeoutParity}, Status: schemas.GuardFail}}}}
	holds := ExtensionStageHolds(held)
	if len(holds) != 1 || !strings.Contains(holds[0], RuleTimeoutParity) {
		t.Fatalf("holds %v", holds)
	}
	res := &schemas.ExtensionResult{Decision: schemas.DecisionImproved}
	HoldImprovement(res, holds)
	if res.Decision != schemas.DecisionInconclusive {
		t.Fatalf("combined %s, want inconclusive", res.Decision)
	}
	reg := &schemas.ExtensionResult{Decision: schemas.DecisionRegressed}
	HoldImprovement(reg, holds)
	if reg.Decision != schemas.DecisionRegressed {
		t.Fatalf("regressed combination changed to %s", reg.Decision)
	}
	clean := &schemas.Analysis{ExpID: "e_p", Verdict: schemas.Verdict{Decision: schemas.DecisionInconclusive, Action: schemas.ActionNoDecision}}
	if h := ExtensionStageHolds(clean); len(h) != 0 {
		t.Fatalf("an ordinary inconclusive stage holds: %v", h)
	}
}

// Audit qc2-03-04: a no_regression guard on another metric of the same
// condition uses the planned alpha (0.025) too. Six A-only wins give
// p = 0.03125 on safe_success: no REGRESSED at the planned threshold.
func TestOtherMetricGuardUsesPlannedAlpha(t *testing.T) {
	clean := schemas.ConditionClean
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: clean, Trials: 1}}
	m.Experiment.Trials = &schemas.TrialsPolicy{Extension: &schemas.ExtensionRule{Rule: "inverse_normal", AlphaFirst: 0.025}}
	m.Experiment.Guards = []schemas.GuardSpec{{Condition: clean, Rule: RuleNoRegression, Metric: "safe_success"}}
	a := passes(clean, ArmBaseline, 1, 1, 1, 1, 1, 1, 1)
	b := passes(clean, ArmCandidate, 1, 0, 0, 0, 0, 0, 0)
	var av, bv []schemas.Value
	for range a {
		av = append(av, schemas.Observed(1, schemas.EvidenceOutcome))
		bv = append(bv, schemas.Observed(0, schemas.EvidenceOutcome))
	}
	a, b = withMetric(a, "safe_success", av...), withMetric(b, "safe_success", bv...)
	an := decideOrFail(t, m, concat(a, b))
	if an.Verdict.Decision != schemas.DecisionInconclusive {
		t.Fatalf("decision %s, want inconclusive at alpha 0.025; guards %+v", an.Verdict.Decision, an.Verdict.Guards)
	}
}

// Audit qc2-03-05: equal task ids under a re-locked suite (a changed probe
// or verifier) are not the same comparison.
func TestExtensionRefusesChangedEvaluator(t *testing.T) {
	p, pr, c, cr := improvingExtension()
	relocked := *c
	relocked.SuiteLockHash = "sha256:relocked"
	relocked.FingerprintHash = "sha256:fp-new"
	if _, err := CombineExtension(p, pr, schemas.DecisionInconclusive, &relocked, cr, schemas.DecisionInconclusive); err == nil || !strings.Contains(err.Error(), "suite lock differs") {
		t.Fatalf("re-locked child combined: %v", err)
	}
	scen := *c
	scen.Scenario = map[string]string{"verifier_mode": "sha256:other"}
	if _, err := CombineExtension(p, pr, schemas.DecisionInconclusive, &scen, cr, schemas.DecisionInconclusive); err == nil || !strings.Contains(err.Error(), "scenario inputs") {
		t.Fatalf("child with other scenario inputs combined: %v", err)
	}
}
