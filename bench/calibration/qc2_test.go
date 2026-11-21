package calibration

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// withJudge sets the judge model, template and served id of runs.
func withJudge(runs []schemas.SealedRun, model, template, served string) []schemas.SealedRun {
	for k := range runs {
		j := *runs[k].Grade.Judge
		j.Model, j.PromptSHA256, j.ServedModel = model, template, served
		g := *runs[k].Grade
		g.Judge = &j
		runs[k].Grade = &g
	}
	return runs
}

// modelRecord is testRecord for another judge model, with human statistics
// for each of templates (all the same items).
func modelRecord(model string, served string, pos, okPos, neg, okNeg int, templates ...string) *schemas.CalibrationRecord {
	r := testRecord(pos, okPos, neg, okNeg, t0)
	r.Model, r.ServedModels = model, []string{served}
	r.RecordID = "cr_" + model
	r.Templates = map[string]string{}
	r.ByTemplate = nil
	for k, t := range templates {
		r.Templates[string(rune('a'+k))] = t
		r.ByTemplate = append(r.ByTemplate, schemas.CalibrationCategory{Category: t, Stats: r.Human})
	}
	return r
}

// QC2-25-01: allow_uncalibrated_judge never turns a measured reliability
// failure of another judge into an allowed result, in either order.
func TestAllowUncalibratedKeepsUnreliableJudgeInvalid(t *testing.T) {
	for name, runs := range map[string][]schemas.SealedRun{
		"uncalibrated majority": append(withJudge(armRuns("A", 2, 0), "x", "aaa", ""), withJudge(armRuns("B", 1, 0), "y", "aaa", "")...),
		"unreliable majority":   append(withJudge(armRuns("A", 1, 0), "x", "aaa", ""), withJudge(armRuns("B", 2, 0), "y", "aaa", "")...),
	} {
		t.Run(name, func(t *testing.T) {
			an := analysis(schemas.DecisionImproved, 0.3)
			Apply(an, Input{Manifest: judgedManifest(true), Runs: runs, Records: records(modelRecord("y", "", 30, 30, 30, 10, "aaa")), Now: now})
			if an.JudgeCalibration.State != schemas.CalibrationStateUnreliable || an.Verdict.Decision != schemas.DecisionInvalid || an.Verdict.Action != schemas.ActionFixHarness {
				t.Fatalf("state %s decision %s action %s (%s)", an.JudgeCalibration.State, an.Verdict.Decision, an.Verdict.Action, an.JudgeCalibration.Detail)
			}
		})
	}
}

// QC2-25-02: each judge needs coverage only for the templates it used.
func TestEachJudgeCoversOnlyItsOwnTemplates(t *testing.T) {
	runs := append(withJudge(armRuns("A", 2, 0), "x", "aaa", ""), withJudge(armRuns("B", 1, 0), "y", "bbb", "")...)
	an := analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: runs, Now: now,
		Records: records(modelRecord("x", "", 30, 30, 30, 30, "aaa", "bbb"), modelRecord("y", "", 30, 30, 30, 30, "bbb"))})
	if an.JudgeCalibration.State != schemas.CalibrationStateCalibrated || an.Verdict.Decision != schemas.DecisionImproved {
		t.Fatalf("state %s decision %s (%s)", an.JudgeCalibration.State, an.Verdict.Decision, an.JudgeCalibration.Detail)
	}
}

// QC2-25-04: a template the record saw only in admission seeds has no human
// evidence, so a grade with it is not covered.
func TestSeedOnlyTemplateIsNotCovered(t *testing.T) {
	rec := testRecord(30, 30, 30, 30, t0) // templates aaa and bbb, human items only use aaa
	if TemplateStats(rec, "aaa") == nil || TemplateStats(rec, "bbb") != nil {
		t.Fatalf("by template %+v", rec.ByTemplate)
	}
	runs := withJudge(append(armRuns("A", 2, 0), armRuns("B", 2, 0)...), "judge-model", "bbb", "")
	an := analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: runs, Records: records(rec), Now: now})
	if an.JudgeCalibration.State != schemas.CalibrationStateUncalibrated || an.Verdict.Decision != schemas.DecisionInconclusive {
		t.Fatalf("state %s decision %s", an.JudgeCalibration.State, an.Verdict.Decision)
	}
}

// QC2-25-05: a record for another provider or vote count does not cover.
func TestRecordMustMatchProviderAndVotes(t *testing.T) {
	rec := testRecord(30, 30, 30, 30, t0) // openai, 1 vote
	set := records(rec)
	for name, id := range map[string]JudgeID{
		"other provider": {Provider: "anthropic", Model: "judge-model", Votes: 1},
		"other votes":    {Provider: "openai", Model: "judge-model", Votes: 3},
	} {
		if cov, _ := set.Covering(id, []string{"aaa"}); cov != nil {
			t.Errorf("%s: covered by %s", name, cov.RecordID)
		}
	}
	if cov, _ := set.Covering(JudgeID{Provider: "openai", Model: "judge-model", Votes: 1}, []string{"aaa"}); cov == nil {
		t.Fatal("the measured configuration is covered")
	}
}

// QC2-25-07: drift is checked per judge against its own record, and a
// mixture of judges is not bias-corrected with one judge's error rates.
func TestMixedJudgeDiagnosticsPerJudge(t *testing.T) {
	runs := append(withJudge(armRuns("A", 2, 0), "x", "aaa", "x-20260101"), withJudge(armRuns("B", 1, 0), "y", "aaa", "y-20260101")...)
	an := analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: runs, Now: now,
		Records: records(modelRecord("x", "x-20260101", 30, 30, 30, 30, "aaa"), modelRecord("y", "y-20260101", 30, 30, 30, 30, "aaa"))})
	jc := an.JudgeCalibration
	for _, r := range jc.Reminders {
		if strings.Contains(r, "may have changed") {
			t.Fatalf("false drift reminder %q", r)
		}
	}
	for arm, b := range jc.BiasAdjusted {
		if b.Estimate != nil || !strings.Contains(b.Note, "several judge configurations") {
			t.Fatalf("arm %s corrected with one judge's rates: %+v", arm, b)
		}
	}
}

// QC2-25-08: a reviewed run's pass is a person's label, not the judge's,
// and is left out of the raw judged rate.
func TestReviewedRunsLeftOutOfJudgedRate(t *testing.T) {
	runs := append(armRuns("A", 2, 0), armRuns("B", 2, 0)...)
	runs[0].Review = &schemas.Review{ReviewID: "rv_1", Decision: schemas.ReviewFail}
	runs[0].Grade.Passed = false
	got := biasAdjusted(runs, schemas.ConditionClean, testRecord(30, 30, 30, 30, t0), false)
	if a := got["A"]; a.Raw.N != 1 || a.Raw.K != 1 {
		t.Fatalf("arm A raw %+v", a.Raw)
	}
}
