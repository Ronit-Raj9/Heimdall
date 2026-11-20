package verdict

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Audit 1-C-01: B's runs graded a pass and then sealed infra_error (cleanup
// failed) still carried verified_success observed 1. They must count as
// unknown, so B has no observed outcomes and the verdict is not IMPROVED.
func TestInfraErrorPassDoesNotVote(t *testing.T) {
	clean := schemas.ConditionClean
	a := passes(clean, ArmBaseline, 1, 0, 0, 0, 0, 0, 0, 0, 0)
	b := passes(clean, ArmCandidate, 1, 1, 1, 1, 1, 1, 1, 1, 1)
	for i := range b {
		b[i].Run.Outcome = schemas.OutcomeInfraError
	}
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: clean, Trials: 1}}
	an := decideOrFail(t, m, concat(a, b))
	if an.Verdict.Decision == schemas.DecisionImproved {
		t.Fatalf("infra_error runs voted: decision %s", an.Verdict.Decision)
	}
	if v := metricValue(b[0], MetricVerifiedSuccess); v.IsObserved() {
		t.Fatalf("infra_error verified_success observed: %+v", v)
	}
}

// reported is an agent-reported observed value, as metrics/reported.go seals.
func reported(x float64) schemas.Value {
	v := schemas.Observed(x, schemas.EvidenceNative)
	v.Source, v.EvidenceLevel = schemas.EvidenceSourceAgentReported, schemas.EvidencePartial
	return v
}

// Audit 1-C-02: an endpoint made only of agent-reported steps (A reports 2,
// B reports 1 on eight tasks) must not decide IMPROVED.
func TestAgentReportedValuesCannotDecide(t *testing.T) {
	clean := schemas.ConditionClean
	a := passes(clean, ArmBaseline, 1, 1, 1, 1, 1, 1, 1, 1, 1)
	b := passes(clean, ArmCandidate, 1, 1, 1, 1, 1, 1, 1, 1, 1)
	var av, bv []schemas.Value
	for range a {
		av, bv = append(av, reported(2)), append(bv, reported(1))
	}
	a, b = withMetric(a, "reported_steps", av...), withMetric(b, "reported_steps", bv...)
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: clean, Trials: 1}}
	m.Experiment.Primary.Metric = "reported_steps"
	an := decideOrFail(t, m, concat(a, b))
	if d := an.Verdict.Decision; d == schemas.DecisionImproved || d == schemas.DecisionRegressed {
		t.Fatalf("agent-reported metric decided %s", d)
	}
}

// Audit 1-C-06: coverage of a no_regression guard must be checked on the
// guard's own metric, not on the primary metric.
func TestGuardCoverageUsesGuardMetric(t *testing.T) {
	clean := schemas.ConditionClean
	const guardMetric = "barge_in_handled"
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: clean, Trials: 1}, {ID: condStale, Trials: 1}}
	m.Experiment.Guards = []schemas.GuardSpec{{Condition: condStale, Rule: RuleNoRegression, Metric: guardMetric}}
	base := concat(passes(clean, ArmBaseline, 1, 0, 0, 0, 0, 0, 0, 0, 0), passes(clean, ArmCandidate, 1, 1, 1, 1, 1, 1, 1, 1, 1))
	probe := func(observed bool) []schemas.SealedRun {
		runs := concat(passes(condStale, ArmBaseline, 1, 1, 1, 1, 1, 1, 1, 1, 1), passes(condStale, ArmCandidate, 1, 1, 1, 1, 1, 1, 1, 1, 1))
		for i := range runs {
			v := schemas.Unknown("not recorded on this probe", schemas.EvidenceOutcome)
			if observed {
				v = schemas.Observed(1, schemas.EvidenceOutcome)
			}
			// verified_success is unobservable on the probe in the
			// observed case, and present in the missing case.
			ms := map[string]schemas.Value{guardMetric: v}
			if !observed {
				ms[MetricVerifiedSuccess] = schemas.Observed(1, schemas.EvidenceOutcome)
			}
			runs[i].Metrics = ms
		}
		return runs
	}
	an := decideOrFail(t, m, concat(base, probe(true)))
	for _, r := range an.Verdict.InvalidReasons {
		if strings.Contains(r, condStale) {
			t.Fatalf("fully observed guard invalidated by unused primary metric: %v", an.Verdict.InvalidReasons)
		}
	}
	an = decideOrFail(t, m, concat(base, probe(false)))
	if an.Verdict.Decision != schemas.DecisionInvalid || !strings.Contains(strings.Join(an.Verdict.InvalidReasons, "\n"), guardMetric) {
		t.Fatalf("sparse guard metric escaped coverage: %s %v", an.Verdict.Decision, an.Verdict.InvalidReasons)
	}
}

// Audit 1-B-01: the default clean guard re-tests the primary endpoint; it
// must use the planned first-stage alpha (0.025), not the nominal 0.05.
// Six tasks where A passes and B fails give p = 2/64 = 0.03125.
func TestDefaultGuardUsesPrimaryThreshold(t *testing.T) {
	clean := schemas.ConditionClean
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: clean, Trials: 1}}
	m.Experiment.Trials = &schemas.TrialsPolicy{Extension: &schemas.ExtensionRule{Rule: "inverse_normal", AlphaFirst: 0.025}}
	runs := concat(passes(clean, ArmBaseline, 1, 1, 1, 1, 1, 1, 1), passes(clean, ArmCandidate, 1, 0, 0, 0, 0, 0, 0))
	an := decideOrFail(t, m, runs)
	if an.Verdict.Decision != schemas.DecisionInconclusive {
		t.Fatalf("decision %s, want inconclusive at alpha 0.025; guards %+v", an.Verdict.Decision, an.Verdict.Guards)
	}
}

// improvingExtension is a declared parent and child where B wins 6 of 8
// tasks in each stage, so the combination alone says IMPROVED.
func improvingExtension() (*schemas.Manifest, []schemas.SealedRun, *schemas.Manifest, []schemas.SealedRun) {
	out := func(task, arm string, _ int) float64 {
		if task == "t07" || task == "t08" || task == "t15" || task == "t16" {
			return 1
		}
		return map[bool]float64{true: 1, false: 0}[arm == ArmCandidate]
	}
	return extendPair(8, 8, true, out)
}

// Audit 1-B-03: a child with another arm config, or one that reruns the
// parent's tasks as "fresh", is not the same comparison; and only the first
// planned child of a parent is bound to the combination.
func TestExtensionBoundToComparison(t *testing.T) {
	p, pr, c, cr := improvingExtension()
	if res, err := CombineExtension(p, pr, schemas.DecisionInconclusive, c, cr, schemas.DecisionInconclusive); err != nil || res.Decision != schemas.DecisionImproved {
		t.Fatalf("baseline combination %+v %v", res, err)
	}
	changed := *c
	changed.Arms = map[string]schemas.ArmManifest{ArmBaseline: {ConfigHash: "x"}, ArmCandidate: {ConfigHash: "other"}}
	if _, err := CombineExtension(p, pr, schemas.DecisionInconclusive, &changed, cr, schemas.DecisionInconclusive); err == nil {
		t.Fatal("child with different arm configs combined")
	}
	rerun := *c
	rerun.Slots = append(append([]schemas.Slot(nil), c.Slots...), p.Slots[0])
	if _, err := CombineExtension(p, pr, schemas.DecisionInconclusive, &rerun, cr, schemas.DecisionInconclusive); err == nil {
		t.Fatal("tasks follow-up rerunning a parent task combined")
	}
	first, second := *c, *c
	first.ExpID, first.CreatedAt = "e_child_a", testFrozenAt
	second.ExpID, second.CreatedAt = "e_child_b", testFrozenAt.Add(1)
	if got := AuthorizedFollowUp(p, []*schemas.Manifest{&second, &first}); got != "e_child_a" {
		t.Fatalf("authorized follow-up %q", got)
	}
}

// Audit 1-B-09: a stage capped by an uncalibrated judge withholds the
// combined directional decision.
func TestExtensionHeldByUncalibratedStage(t *testing.T) {
	p, pr, c, cr := improvingExtension()
	res, err := CombineExtension(p, pr, schemas.DecisionInconclusive, c, cr, schemas.DecisionInconclusive)
	if err != nil || res.Decision != schemas.DecisionImproved {
		t.Fatalf("baseline %+v %v", res, err)
	}
	pa := &schemas.Analysis{ExpID: p.ExpID, JudgeCalibration: &schemas.JudgeCalibrationVerdict{State: schemas.CalibrationStateUncalibrated, Caps: true, CappedFrom: schemas.DecisionImproved}}
	ca := &schemas.Analysis{ExpID: c.ExpID, JudgeCalibration: &schemas.JudgeCalibrationVerdict{State: schemas.CalibrationStateUncalibrated, Caps: true}}
	HoldExtension(res, append(ExtensionStageProblems(pa), ExtensionStageProblems(ca)...))
	if res.Decision != schemas.DecisionInconclusive || len(res.Reasons) < 3 {
		t.Fatalf("uncalibrated stages decided %+v", res)
	}
	if got := ExtensionStageProblems(&schemas.Analysis{ExpID: "ok"}); len(got) != 0 {
		t.Fatalf("clean stage problems %v", got)
	}
}

// Audit 1-B-10: every planned clean A2 slot is missing, so the A/A check
// observed nothing; it must be unknown and hold the release, not pass.
func TestMissingAANotPass(t *testing.T) {
	clean := schemas.ConditionClean
	m := testManifest(ArmBaseline, ArmCandidate, ArmAA)
	m.Experiment.Primary = schemas.Endpoint{Condition: condStale, Metric: MetricVerifiedSuccess}
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: clean, Trials: 1}, {ID: condStale, Trials: 1}}
	runs := concat(
		passes(condStale, ArmBaseline, 1, 0, 0, 0, 0, 0, 0, 0, 0),
		passes(condStale, ArmCandidate, 1, 1, 1, 1, 1, 1, 1, 1, 1),
		passes(clean, ArmBaseline, 1, 1, 1),
		passes(clean, ArmCandidate, 1, 1, 1),
	)
	aa := passes(clean, ArmAA, 1, 1, 1)
	m = withSlots(m, concat(runs, aa)) // A2 slots planned, never sealed
	an := decideOrFail(t, m, runs)
	check := checkNamed(an.Verdict.Instruments, InstrumentAANoiseFloor)
	if check == nil || check.Status != schemas.InstrumentUnknown {
		t.Fatalf("aa_noise_floor %+v", check)
	}
	if an.Verdict.Action == schemas.ActionShip {
		t.Fatalf("shipped with no A/A evidence: %s/%s", an.Verdict.Decision, an.Verdict.Action)
	}
}
