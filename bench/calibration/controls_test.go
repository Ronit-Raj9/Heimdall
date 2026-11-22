package calibration

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// controlRecord is a record whose items are admission controls only:
// pos answers labelled correct (okPos judged correct) and neg labelled
// incorrect (okNeg judged incorrect), plus humanPos/humanNeg human labels
// all judged right.
func controlRecord(pos, okPos, neg, okNeg, humanPos, humanNeg int) *schemas.CalibrationRecord {
	rec := &schemas.CalibrationRecord{
		Suite: "qa", SuiteVersion: "1.0.0", Provider: "openai", Model: "judge-model", PromptVersion: "qa-v1",
		Templates: map[string]string{"qa-v1-answer": "aaa", "qa-v1-rubric": "bbb"}, Votes: 1,
		ServedModels: []string{"judge-model-2026-01-01"}, SetSHA256: "set1", CreatedAt: t0,
	}
	rec.PromptSHA256 = PromptHash(rec.Templates)
	seeds := itemRuns(pos, okPos, neg, okNeg)
	for k := range seeds {
		seeds[k].Item.Seed, seeds[k].Item.Item.Source = true, schemas.CalibrationSourceControl
	}
	human := itemRuns(humanPos, humanPos, humanNeg, humanNeg)
	for k := range human {
		human[k].Item.Item.Answer += "-human"
		human[k].Item.Item.ID = ItemID("t1", human[k].Item.Item.Answer)
	}
	Build(rec, append(seeds, human...), 0)
	return rec
}

func TestCalibratedOnControls(t *testing.T) {
	runs := append(armRuns("A", 5, 5), armRuns("B", 8, 2)...)
	tests := []struct {
		name   string
		rec    *schemas.CalibrationRecord
		state  string
		want   schemas.Decision
		detail string
	}{
		// 24 controls all agree: agreement lower bound 0.858, TNR (12/12) 0.735, TPR the same.
		{name: "enough agreeing controls stand", rec: controlRecord(12, 12, 12, 12, 0, 0), state: schemas.CalibrationStateControls,
			want: schemas.DecisionImproved, detail: "judge calibrated on controls"},
		{name: "too few controls cap", rec: controlRecord(6, 6, 6, 6, 0, 0), state: schemas.CalibrationStateUncalibrated,
			want: schemas.DecisionInconclusive, detail: "fewer than the 20 needed"},
		{name: "controls the judge gets wrong cap", rec: controlRecord(12, 12, 12, 6, 0, 0), state: schemas.CalibrationStateUncalibrated,
			want: schemas.DecisionInconclusive, detail: "disagrees with its controls"},
		{name: "only wrong answers cap", rec: controlRecord(0, 0, 24, 24, 0, 0), state: schemas.CalibrationStateUncalibrated,
			want: schemas.DecisionInconclusive, detail: "labelled correct and answers labelled incorrect"},
		{name: "a few human labels fall back to controls", rec: controlRecord(12, 12, 12, 12, 3, 3), state: schemas.CalibrationStateControls,
			want: schemas.DecisionImproved, detail: "6 human labels so far"},
		{name: "enough human labels are human-calibrated", rec: controlRecord(12, 12, 12, 12, 30, 30), state: schemas.CalibrationStateCalibrated,
			want: schemas.DecisionImproved, detail: "judge calibrated on 60 human labels"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			an := analysis(schemas.DecisionImproved, 0.3)
			Apply(an, Input{Manifest: judgedManifest(false), Runs: runs, Records: records(tt.rec), SetSHA256: "set1", Now: now})
			jc := an.JudgeCalibration
			if jc == nil || jc.State != tt.state {
				t.Fatalf("state %+v, want %s", jc, tt.state)
			}
			if an.Verdict.Decision != tt.want {
				t.Fatalf("decision %s, want %s (%s)", an.Verdict.Decision, tt.want, jc.Detail)
			}
			if !strings.Contains(jc.Detail, tt.detail) {
				t.Fatalf("detail %q, want %q", jc.Detail, tt.detail)
			}
			if tt.state == schemas.CalibrationStateControls {
				if jc.Caps || jc.ControlItems != 24 || jc.Agreement == nil || jc.Agreement.N != 24 {
					t.Fatalf("controls verdict %+v", jc)
				}
				if ins := instrument(t, an); ins.Status != schemas.InstrumentWarn {
					t.Fatalf("instrument %+v", ins)
				}
			}
		})
	}
}

func TestControlsMustCoverEveryTemplate(t *testing.T) {
	rec := controlRecord(12, 12, 12, 12, 0, 0)
	if _, why := controlsEvidence(rec, []string{"aaa"}, Thresholds(nil, "")); why != "" {
		t.Fatalf("aaa covered: %s", why)
	}
	if _, why := controlsEvidence(rec, []string{"aaa", "bbb"}, Thresholds(nil, "")); !strings.Contains(why, "no admission-control item used") {
		t.Fatalf("bbb has no control item: %q", why)
	}
}

func TestMinControlsFromPolicy(t *testing.T) {
	th := Thresholds(&schemas.JudgeCalibrationPolicy{MinControls: 30}, "policy.yaml")
	if th.MinControls != 30 || Thresholds(nil, "").MinControls != DefaultMinControls {
		t.Fatalf("thresholds %+v", th)
	}
	if _, why := controlsEvidence(controlRecord(12, 12, 12, 12, 0, 0), []string{"aaa"}, th); !strings.Contains(why, "fewer than the 30") {
		t.Fatalf("why %q", why)
	}
}
