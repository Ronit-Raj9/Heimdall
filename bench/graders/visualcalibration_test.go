package graders

import (
	"image/color"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X1 vision grading: calibration of the vision judge on labelled images,
// with a scripted vision model, then a grade covered by the record.
func TestVisualCalibrationCoversTheGrade(t *testing.T) {
	g, sv, gw, rc := judgedVisual(t, []string{`{"verdict":"pass","score":1}`}, "", 1)
	s := rc.Suite
	s.Tasks = []*schemas.Task{rc.Task}
	// Two labelled images: the reference chart (correct) and a red one
	// (incorrect), plus a text item the vision calibration skips.
	var items []schemas.CalibrationItem
	for _, c := range []struct {
		col   color.NRGBA
		label string
	}{{barBlue, schemas.CalibrationCorrect}, {color.NRGBA{0xdc, 0x26, 0x26, 255}, schemas.CalibrationIncorrect}} {
		sum, err := calibration.AddImage(s.Root, chartPNG(t, c.col, 30))
		if err != nil {
			t.Fatal(err)
		}
		it := schemas.CalibrationItem{Task: "chart", Image: sum, Label: c.label, Labeller: "ana", Source: schemas.CalibrationSourceLabel, At: time.Now().UTC().Truncate(time.Second)}
		it.ID = calibration.ItemIDOf(it)
		items = append(items, it)
	}
	text := schemas.CalibrationItem{Task: "chart", Answer: "a bar chart", Label: schemas.CalibrationCorrect, Labeller: "ana", Source: schemas.CalibrationSourceLabel}
	text.ID = calibration.ItemIDOf(text)
	if _, err := calibration.Append(s.Root, append(items, text)); err != nil {
		t.Fatal(err)
	}
	set, err := calibration.LoadSet(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	human, _, disputed := calibration.Resolve(set.Items)
	plan, err := PlanVisualCalibration(s, human)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 2 || len(plan.Skipped) != 1 || plan.Calls != 4 || !strings.Contains(plan.Skipped[0], "not an image") {
		t.Fatalf("plan: %d items, skipped %v, calls %d", len(plan.Items), plan.Skipped, plan.Calls)
	}
	// The scripted model says pass to everything: one true positive, one
	// false positive.
	srv := httptest.NewServer(sv)
	defer srv.Close()
	runs, spend, err := RunVisualCalibration(t.Context(), JudgeConfig{Gateway: gw, URL: srv.URL}, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spend.Calls != 4 || len(runs) != 2 || runs[0].Votes[0] != VoteCorrect {
		t.Fatalf("runs %+v spend %+v", runs, spend)
	}
	rec := VisualCalibrationRecord(s, plan, set, runs, spend, disputed, "snap@x")
	if rec.Human.TP != 1 || rec.Human.FP != 1 || rec.Templates[PromptVisualV1] != plan.PromptSHA || rec.Model != "vision-model" {
		t.Fatalf("record %+v", rec.Human)
	}
	if err := calibration.WriteRecord(s.Root, rec); err != nil {
		t.Fatal(err)
	}
	gr, err := g.Grade(t.Context(), rc, &schemas.Diff{})
	if err != nil {
		t.Fatal(err)
	}
	if j := gr.Visual.Judge; !j.Decides || j.Calibration != calibration.Ref(rec) || j.Agreement.FP != 1 || gr.Judge.Calibration != j.Calibration {
		t.Fatalf("judge after calibration %+v", j)
	}
}

// TestVisualJudgeGoesThroughTheInstrument grades visual runs with an
// uncalibrated vision judge and feeds them to W8's judge_calibration
// instrument: an IMPROVED verdict is capped at INCONCLUSIVE, keyed by the
// vision model and the visual-v1 template; with a record it is not.
func TestVisualJudgeGoesThroughTheInstrument(t *testing.T) {
	g, _, _, rc := judgedVisual(t, []string{`{"verdict":"pass","score":1}`}, "", 1)
	gr, err := g.Grade(t.Context(), rc, &schemas.Diff{})
	if err != nil {
		t.Fatal(err)
	}
	runs := []schemas.SealedRun{{Slot: schemas.Slot{Counted: true, Arm: "A"}, Run: schemas.Run{RunID: "r_a"}, Grade: gr},
		{Slot: schemas.Slot{Counted: true, Arm: "B"}, Run: schemas.Run{RunID: "r_b"}, Grade: gr}}
	improved := func() *schemas.Analysis {
		return &schemas.Analysis{Verdict: schemas.Verdict{Decision: schemas.DecisionImproved, Action: schemas.ActionShip}}
	}
	an := improved()
	calibration.Apply(an, calibration.Input{Manifest: &schemas.Manifest{}, Runs: runs})
	_, sha, _ := VisualPrompt(PromptVisualV1)
	jc := an.JudgeCalibration
	if jc == nil || jc.State != schemas.CalibrationStateUncalibrated || jc.Model != "vision-model" || len(jc.Templates) != 1 || jc.Templates[0] != sha {
		t.Fatalf("instrument %+v", jc)
	}
	if an.Verdict.Decision != schemas.DecisionInconclusive || jc.CappedFrom != schemas.DecisionImproved {
		t.Fatalf("verdict %s, capped from %s", an.Verdict.Decision, jc.CappedFrom)
	}
	rec := &schemas.CalibrationRecord{RecordID: "cr_visual2", Model: "vision-model", PromptVersion: PromptVisualV1, Templates: map[string]string{PromptVisualV1: sha},
		HumanLabels: 40, Human: schemas.CalibrationStats{Items: 40, Agreement: calibration.Rate(39, 40), TNR: calibration.Rate(20, 20), FPR: calibration.Rate(0, 20)},
		CreatedAt: time.Now().UTC()}
	if err := calibration.WriteRecord(rc.Suite.Root, rec); err != nil {
		t.Fatal(err)
	}
	recs, err := calibration.ReadRecords(rc.Suite.Root)
	if err != nil {
		t.Fatal(err)
	}
	an = improved()
	calibration.Apply(an, calibration.Input{Manifest: &schemas.Manifest{}, Runs: runs, Records: recs})
	if an.JudgeCalibration.State != schemas.CalibrationStateCalibrated || an.Verdict.Decision != schemas.DecisionImproved {
		t.Fatalf("with a record: %s, %s (%s)", an.JudgeCalibration.State, an.Verdict.Decision, an.JudgeCalibration.Detail)
	}
}

// TestVisualJudged covers 7-C-03: only a task with a rubric and a pinned
// vision judge can take a calibration label.
func TestVisualJudged(t *testing.T) {
	_, _, _, rc := judgedVisual(t, []string{`{"verdict":"pass","score":1}`}, "", 1)
	if !VisualJudged(rc.Suite, rc.Task) {
		t.Fatal("judged task not recognised")
	}
	det := &schemas.Task{ID: "det", Output: &schemas.OutputSpec{Visual: &schemas.VisualCheck{}}}
	if VisualJudged(&schemas.Suite{}, det) || VisualJudged(rc.Suite, nil) {
		t.Fatal("deterministic-only or missing task accepted")
	}
}
