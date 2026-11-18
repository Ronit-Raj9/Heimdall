package calibration

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// judgedRun is a counted clean run of arm, graded by the judge with votes.
func judgedRun(id, arm string, votes []string, served string) schemas.SealedRun {
	maj := Majority(votes)
	return schemas.SealedRun{
		Slot:     schemas.Slot{Counted: true, Arm: arm, Condition: schemas.ConditionClean, Task: "t1"},
		Run:      schemas.Run{RunID: id},
		SealHash: "seal-" + id,
		Grade: &schemas.Grade{Judged: true, Passed: maj == c, Judge: &schemas.JudgeRecord{
			Model: "judge-model", PromptSHA256: "aaa", Votes: votes, ServedModel: served,
		}},
	}
}

func armRuns(arm string, pass, fail int) []schemas.SealedRun {
	var out []schemas.SealedRun
	for k := 0; k < pass+fail; k++ {
		v := c
		if k >= pass {
			v = i
		}
		out = append(out, judgedRun(arm+string(rune('a'+k)), arm, []string{v}, "judge-model-2026-01-01"))
	}
	return out
}

func judgedManifest(allow bool) *schemas.Manifest {
	m := &schemas.Manifest{ExpID: "e_x", SuiteID: "qa", Scenario: map[string]string{"suite.judge": "h"}}
	m.Experiment.Verdict.AllowUncalibratedJudge = allow
	return m
}

func analysis(d schemas.Decision, dbar float64) *schemas.Analysis {
	a := schemas.ActionShip
	if d == schemas.DecisionRegressed {
		a = schemas.ActionHold
	}
	return &schemas.Analysis{Verdict: schemas.Verdict{Decision: d, Action: a, Primary: schemas.Endpoint{Condition: schemas.ConditionClean},
		Effect: &schemas.Effect{DBar: dbar}}}
}

func records(recs ...*schemas.CalibrationRecord) *RecordSet {
	s := &RecordSet{Verified: true}
	for _, r := range recs {
		s.Valid = append(s.Valid, *r)
	}
	return s
}

var now = t0.Add(10 * 24 * time.Hour)

func TestApplyLeavesVerifiedExperimentsAlone(t *testing.T) {
	an := analysis(schemas.DecisionImproved, 0.2)
	before := *an
	runs := []schemas.SealedRun{{Slot: schemas.Slot{Counted: true}, Grade: &schemas.Grade{Passed: true}}}
	Apply(an, Input{Manifest: &schemas.Manifest{}, Runs: runs, Now: now})
	if !reflect.DeepEqual(*an, before) {
		t.Fatalf("a verified experiment changed: %+v", an.Verdict)
	}
}

func instrument(t *testing.T, an *schemas.Analysis) schemas.InstrumentCheck {
	t.Helper()
	for _, c := range an.Verdict.Instruments {
		if c.Name == InstrumentName {
			return c
		}
	}
	t.Fatalf("no %s instrument in %+v", InstrumentName, an.Verdict.Instruments)
	return schemas.InstrumentCheck{}
}

func TestApplyStates(t *testing.T) {
	runs := append(armRuns("A", 5, 5), armRuns("B", 8, 2)...)
	tests := []struct {
		name     string
		recs     *RecordSet
		allow    bool
		decision schemas.Decision
		state    string
		status   schemas.InstrumentStatus
		want     schemas.Decision
		action   schemas.Action
		detail   string
	}{
		{name: "no record caps improved", recs: records(), decision: schemas.DecisionImproved, state: schemas.CalibrationStateUncalibrated,
			status: schemas.InstrumentUnknown, want: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, detail: "judge not calibrated"},
		{name: "no record caps regressed and holds", recs: records(), decision: schemas.DecisionRegressed, state: schemas.CalibrationStateUncalibrated,
			status: schemas.InstrumentUnknown, want: schemas.DecisionInconclusive, action: schemas.ActionHold},
		{name: "record of another template", recs: records(func() *schemas.CalibrationRecord {
			r := testRecord(30, 30, 30, 30, t0)
			r.Templates = map[string]string{"qa-v1-rubric": "bbb"}
			return r
		}()), decision: schemas.DecisionImproved, state: schemas.CalibrationStateUncalibrated, status: schemas.InstrumentUnknown,
			want: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, detail: "does not cover it"},
		{name: "allowed uncalibrated is labelled", recs: records(), allow: true, decision: schemas.DecisionImproved, state: schemas.CalibrationStateAllowed,
			status: schemas.InstrumentWarn, want: schemas.DecisionImproved, action: schemas.ActionShip, detail: "allow_uncalibrated_judge"},
		{name: "seeds only is uncalibrated", recs: records(func() *schemas.CalibrationRecord {
			r := testRecord(0, 0, 0, 0, t0)
			r.SeedItems = 12
			return r
		}()), decision: schemas.DecisionImproved, state: schemas.CalibrationStateUncalibrated, status: schemas.InstrumentUnknown,
			want: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, detail: "no human labels"},
		// 20 of 20 agree: agreement lower bound 0.832, TNR (10/10) 0.692: passes, fewer than 30 labels warns.
		{name: "few labels passing warns", recs: records(testRecord(10, 10, 10, 10, t0)), decision: schemas.DecisionImproved,
			state: schemas.CalibrationStateFewLabels, status: schemas.InstrumentWarn, want: schemas.DecisionImproved, action: schemas.ActionShip, detail: "fewer than 30"},
		// 10 of 10 agree: the agreement lower bound 0.692 is under 0.75: cannot be shown reliable yet, so capped.
		{name: "few labels failing caps", recs: records(testRecord(5, 5, 5, 5, t0)), decision: schemas.DecisionImproved,
			state: schemas.CalibrationStateFewLabels, status: schemas.InstrumentWarn, want: schemas.DecisionInconclusive, action: schemas.ActionNoDecision, detail: "too few labels"},
		// 30 of 40 agree: lower bound 0.588: INVALID.
		{name: "unreliable is invalid", recs: records(testRecord(20, 15, 20, 15, t0)), decision: schemas.DecisionImproved,
			state: schemas.CalibrationStateUnreliable, status: schemas.InstrumentFail, want: schemas.DecisionInvalid, action: schemas.ActionFixHarness, detail: "judge unreliable"},
		{name: "no negative labels is not shown reliable", recs: records(testRecord(40, 40, 0, 0, t0)), decision: schemas.DecisionImproved,
			state: schemas.CalibrationStateUnreliable, status: schemas.InstrumentFail, want: schemas.DecisionInvalid, action: schemas.ActionFixHarness, detail: "TNR not measured"},
		{name: "calibrated passes", recs: records(testRecord(30, 30, 30, 30, t0)), decision: schemas.DecisionImproved,
			state: schemas.CalibrationStateCalibrated, status: schemas.InstrumentPass, want: schemas.DecisionImproved, action: schemas.ActionShip, detail: "judge calibrated on 60"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			an := analysis(tt.decision, 0.3)
			Apply(an, Input{Manifest: judgedManifest(tt.allow), Runs: runs, Records: tt.recs, SetSHA256: "set1", Now: now})
			jc := an.JudgeCalibration
			if jc == nil || jc.State != tt.state {
				t.Fatalf("state %+v, want %s", jc, tt.state)
			}
			ins := instrument(t, an)
			if ins.Status != tt.status || !strings.Contains(ins.Detail, tt.detail) {
				t.Fatalf("instrument %+v, want %s containing %q", ins, tt.status, tt.detail)
			}
			if an.Verdict.Decision != tt.want || an.Verdict.Action != tt.action {
				t.Fatalf("decision %s action %s, want %s %s", an.Verdict.Decision, an.Verdict.Action, tt.want, tt.action)
			}
			if tt.want == schemas.DecisionInconclusive && jc.CappedFrom != tt.decision {
				t.Fatalf("capped_from %q", jc.CappedFrom)
			}
			if tt.want == schemas.DecisionInvalid && (len(an.Verdict.InvalidReasons) != 1 || !strings.Contains(an.Verdict.InvalidReasons[0], "judge unreliable")) {
				t.Fatalf("invalid reasons %v", an.Verdict.InvalidReasons)
			}
			if jc.JudgedRuns != 20 || len(jc.BiasAdjusted) != 2 {
				t.Fatalf("judged %d bias %v", jc.JudgedRuns, jc.BiasAdjusted)
			}
		})
	}
}

func TestApplyThresholdsFromPolicy(t *testing.T) {
	low := 0.5
	an := analysis(schemas.DecisionImproved, 0.3)
	// 30 of 40 agree: unreliable at the default 0.75, fine at 0.5.
	Apply(an, Input{Manifest: judgedManifest(false), Runs: armRuns("B", 3, 1), Records: records(testRecord(20, 15, 20, 15, t0)),
		Policy: &schemas.JudgeCalibrationPolicy{MinAgreement: &low, MinTNR: &low, MinLabels: 10}, PolicySource: "policies/x.yaml", Now: now})
	if an.JudgeCalibration.State != schemas.CalibrationStateCalibrated || an.JudgeCalibration.Thresholds.Source != "policies/x.yaml" || an.Verdict.Decision != schemas.DecisionImproved {
		t.Fatalf("policy thresholds not applied: %+v", an.JudgeCalibration)
	}
	if d := Thresholds(nil, ""); d.MinAgreement != DefaultMinAgreement || d.MinTNR != DefaultMinTNR || d.MinLabels != 30 || d.MaxAgeDays != 90 || d.Source != "defaults" {
		t.Fatalf("defaults %+v", d)
	}
}

func TestDriftReminders(t *testing.T) {
	old := testRecord(30, 30, 30, 30, t0.Add(-200*24*time.Hour))
	runs := append(armRuns("A", 5, 5), judgedRun("Bz", "B", []string{c}, "judge-model-2026-09-30"))
	an := analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: runs, Records: records(old), SetSHA256: "set2", Now: now})
	rem := strings.Join(an.JudgeCalibration.Reminders, "\n")
	for _, want := range []string{"days old", "judge-model-2026-09-30", "labels changed"} {
		if !strings.Contains(rem, want) {
			t.Fatalf("reminders %q lack %q", rem, want)
		}
	}
	if ins := instrument(t, an); ins.Status != schemas.InstrumentWarn || an.Verdict.Decision != schemas.DecisionImproved {
		t.Fatalf("a reminder warns and decides nothing: %+v %s", ins, an.Verdict.Decision)
	}
	fresh := testRecord(30, 30, 30, 30, now.Add(-24*time.Hour))
	if got := Reminders(fresh, Thresholds(nil, ""), "set1", []string{"judge-model-2026-01-01"}, now); len(got) != 0 {
		t.Fatalf("a fresh record has reminders: %v", got)
	}
}

func TestErrorOverEffect(t *testing.T) {
	// 60 of 60 agree: lower bound 0.94, error rate up to 6%.
	rec := testRecord(30, 30, 30, 30, t0)
	an := analysis(schemas.DecisionImproved, 0.04)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: armRuns("B", 3, 1), Records: records(rec), Now: now})
	if !strings.Contains(an.JudgeCalibration.ErrorOverEffect, "exceeds the effect claimed (+4.0 pp)") {
		t.Fatalf("error over effect %q", an.JudgeCalibration.ErrorOverEffect)
	}
	big := analysis(schemas.DecisionImproved, 0.2)
	Apply(big, Input{Manifest: judgedManifest(false), Runs: armRuns("B", 3, 1), Records: records(rec), Now: now})
	if big.JudgeCalibration.ErrorOverEffect != "" {
		t.Fatalf("an effect over the error rate: %q", big.JudgeCalibration.ErrorOverEffect)
	}
}

func TestBiasAdjustedBesideRaw(t *testing.T) {
	rec := testRecord(30, 30, 30, 30, t0) // TPR = TNR = 1: corrected equals raw
	an := analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: append(armRuns("A", 5, 5), armRuns("B", 8, 2)...), Records: records(rec), Now: now})
	b := an.JudgeCalibration.BiasAdjusted["B"]
	if b.Raw.K != 8 || b.Raw.N != 10 || b.Estimate == nil || *b.Estimate != 0.8 {
		t.Fatalf("arm B %+v", b)
	}
	if an.Verdict.Decision != schemas.DecisionImproved {
		t.Fatal("the corrected estimate never replaces the verdict")
	}
}

func TestContested(t *testing.T) {
	split := judgedRun("r1", "A", []string{c, i, c}, "")
	if !Contested(&split, nil) {
		t.Fatal("split votes are contested")
	}
	plain := judgedRun("r2", "A", []string{i}, "")
	if Contested(&plain, nil) {
		t.Fatal("a unanimous, unreviewed run is not contested")
	}
	audited := judgedRun("r3", "A", []string{c}, "")
	audited.Grade.Judge.Audit = &schemas.JudgeAudit{Verdict: i, Agrees: false}
	if !Contested(&audited, nil) {
		t.Fatal("an auditor disagreement is contested")
	}
	audited.Grade.Judge.Audit.Error = "auditor down"
	if Contested(&audited, nil) {
		t.Fatal("a failed audit is not a disagreement")
	}
	review := func(d schemas.ReviewDecision, seal string) []schemas.Review {
		return []schemas.Review{{RunID: "r2", Decision: d, ParentSeal: seal}}
	}
	if !Contested(&plain, review(schemas.ReviewPass, plain.SealHash)) {
		t.Fatal("a pass review of a judged-incorrect run is contested")
	}
	if Contested(&plain, review(schemas.ReviewFail, plain.SealHash)) {
		t.Fatal("a review that agrees is not contested")
	}
	if Contested(&plain, review(schemas.ReviewPass, "other-seal")) {
		t.Fatal("a review written against another seal is ignored")
	}
	if Contested(&plain, append(review(schemas.ReviewPass, plain.SealHash), review(schemas.ReviewFail, plain.SealHash)...)) {
		t.Fatal("the newest decision review counts")
	}
	an := analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: []schemas.SealedRun{split, plain}, Records: records(testRecord(30, 30, 30, 30, t0)),
		Reviews: map[string][]schemas.Review{"r2": review(schemas.ReviewPass, plain.SealHash)}, Now: now})
	if an.JudgeCalibration.ContestedRuns != 2 || !strings.Contains(an.JudgeCalibration.Detail, "2 of 2 judged runs contested") {
		t.Fatalf("contested %d detail %q", an.JudgeCalibration.ContestedRuns, an.JudgeCalibration.Detail)
	}
	if an.Verdict.Decision != schemas.DecisionImproved {
		t.Fatal("contested runs never overrule the verdict")
	}
}

func TestSectionAndNotApplicable(t *testing.T) {
	an := analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: nil, Now: now})
	if an.JudgeCalibration != nil || instrument(t, an).Status != schemas.InstrumentNotApplicable {
		t.Fatalf("no judged run: %+v", an.Verdict.Instruments)
	}
	an = analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: armRuns("B", 3, 1), Records: records(), Now: now})
	sec := Section(an.JudgeCalibration)
	for _, want := range []string{"## Judge calibration", "uncalibrated", "capped at INCONCLUSIVE", "| B |"} {
		if !strings.Contains(sec, want) {
			t.Fatalf("section lacks %q:\n%s", want, sec)
		}
	}
	if Section(nil) != "" {
		t.Fatal("no calibration, no section")
	}
}

func TestStatus(t *testing.T) {
	root := t.TempDir()
	s := &schemas.Suite{ID: "qa", Root: root, Grading: schemas.GradingJudged, Judge: &schemas.JudgeSpec{Provider: "openai", Model: "judge-model", PromptVersion: "qa-v1"}}
	tmpl := map[string]string{"qa-v1-answer": "aaa", "qa-v1-rubric": "bbb"}
	st, err := Status(s, tmpl, Thresholds(nil, ""), now)
	if err != nil || st.State != schemas.CalibrationStateUncalibrated || !st.Judged {
		t.Fatalf("status %+v %v", st, err)
	}
	if _, err := Append(root, []schemas.CalibrationItem{label(t, "t1", "Max", lc, "ana", schemas.CalibrationSourceLabel)}); err != nil {
		t.Fatal(err)
	}
	rec := testRecord(30, 30, 30, 30, t0)
	if err := WriteRecord(root, rec); err != nil {
		t.Fatal(err)
	}
	st, err = Status(s, tmpl, Thresholds(nil, ""), now)
	if err != nil || st.State != schemas.CalibrationStateCalibrated || st.Latest == nil || st.Latest.Items != nil || st.Labels != 1 || st.Records != 1 {
		t.Fatalf("status %+v %v", st, err)
	}
	if len(st.Reminders) != 1 || !strings.Contains(st.Reminders[0], "labels changed") {
		t.Fatalf("reminders %v", st.Reminders)
	}
	s.Grading = ""
	if st, _ := Status(s, nil, Thresholds(nil, ""), now); st.State != StateNotJudged {
		t.Fatalf("verified suite %+v", st)
	}
}
