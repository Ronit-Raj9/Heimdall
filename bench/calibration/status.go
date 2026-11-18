package calibration

import (
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// StateNotJudged is the status of a suite that is not judged.
const StateNotJudged = "not_judged"

// Status is the calibration state of a loaded suite's pinned judge
// (`judge status`, GET /suites/{id}/calibration): the label counts, the
// newest valid record for the pinned model and prompt version (templates
// maps each template of the version to its sha256), its state against the
// thresholds, and drift reminders. It reads files only.
func Status(s *schemas.Suite, templates map[string]string, th schemas.JudgeCalibrationThresholds, now time.Time) (*schemas.CalibrationStatus, error) {
	st := &schemas.CalibrationStatus{Suite: s.ID, Thresholds: th, Reminders: []string{}, Problems: []string{}}
	set, err := LoadSet(s.Root)
	if err != nil {
		return nil, err
	}
	human, seed, disputed := Resolve(set.Items)
	st.SetSHA256, st.Labels, st.HumanLabels, st.SeedItems, st.Disputed = set.SHA256, len(set.Items), len(human), len(seed), disputed
	if s.Grading != schemas.GradingJudged || s.Judge == nil {
		st.State, st.Detail = StateNotJudged, "the suite is not judged; its outcomes come from a deterministic check"
		return st, nil
	}
	j := s.Judge
	st.Judged, st.Provider, st.Model, st.PromptVersion = true, j.Provider, j.Model, j.PromptVersion
	if len(templates) > 0 {
		st.PromptSHA256 = PromptHash(templates)
	}
	recs, err := ReadRecords(s.Root)
	if err != nil {
		return nil, err
	}
	st.Problems = append(st.Problems, recs.Problems...)
	st.Records = len(recs.Valid)
	rec := recs.ForPrompt(j.Model, st.PromptSHA256)
	switch {
	case rec == nil:
		st.State = schemas.CalibrationStateUncalibrated
		st.Detail = fmt.Sprintf("no calibration record for judge %s with prompt %s; judged verdicts are capped at INCONCLUSIVE until `judge calibrate %s` runs", j.Model, j.PromptVersion, s.ID)
		if st.HumanLabels < th.MinLabels {
			st.Detail += fmt.Sprintf(" on at least %d human labels (%d so far)", th.MinLabels, st.HumanLabels)
		}
		return st, nil
	case rec.HumanLabels == 0:
		st.State = schemas.CalibrationStateUncalibrated
		st.Detail = fmt.Sprintf("record %s has admission-control seeds only; label agents' answers (judge label, judge calibrate -from-reviews)", rec.RecordID)
	default:
		bounds, pass := boundsText(rec, th)
		switch {
		case rec.HumanLabels < th.MinLabels:
			st.State = schemas.CalibrationStateFewLabels
			st.Detail = fmt.Sprintf("%d human labels, fewer than %d: %s", rec.HumanLabels, th.MinLabels, bounds)
		case !pass:
			st.State = schemas.CalibrationStateUnreliable
			st.Detail = "judge unreliable: " + bounds
		default:
			st.State = schemas.CalibrationStateCalibrated
			st.Detail = bounds
		}
	}
	st.Latest = Summary(rec)
	st.Reminders = append(st.Reminders, Reminders(rec, th, set.SHA256, nil, now)...)
	return st, nil
}
