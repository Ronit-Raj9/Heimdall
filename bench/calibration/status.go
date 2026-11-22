package calibration

import (
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// StateNotJudged is the status of a suite that is not judged.
const StateNotJudged = "not_judged"

// StatusJudge is the judge a suite's grades are decided by, as `judge
// status` resolves it: the suite-level text judge, or the vision judge its
// task rubrics pin when there is none (QC2-25-06).
type StatusJudge struct {
	ID            JudgeID
	PromptVersion string
	Templates     map[string]string // every template of the prompt version: name -> sha256
	Used          []string          // template hashes the suite's tasks can be graded with (QC2-25-09); empty means all of Templates
}

// Status is the calibration state of a loaded suite's judge (`judge
// status`, GET /suites/{id}/calibration): the label counts, the newest
// valid record that measured that judge configuration and covers every
// template the suite's tasks can use, its state against the thresholds
// (the same assessment as the verdict's judge_calibration instrument), and
// drift reminders. A nil judge, or a suite not graded judged, is
// not_judged. It reads files only.
func Status(s *schemas.Suite, j *StatusJudge, th schemas.JudgeCalibrationThresholds, now time.Time) (*schemas.CalibrationStatus, error) {
	st := &schemas.CalibrationStatus{Suite: s.ID, Thresholds: th, Reminders: []string{}, Problems: []string{}}
	set, err := LoadSet(s.Root)
	if err != nil {
		return nil, err
	}
	human, seed, disputed := Resolve(set.Items)
	st.SetSHA256, st.Labels, st.HumanLabels, st.SeedItems, st.Disputed = set.SHA256, len(set.Items), len(human), len(seed), disputed
	if s.Grading != schemas.GradingJudged || j == nil {
		st.State, st.Detail = StateNotJudged, "the suite is not judged; its outcomes come from a deterministic check"
		return st, nil
	}
	st.Judged, st.Provider, st.Model, st.PromptVersion = true, j.ID.Provider, j.ID.Model, j.PromptVersion
	if len(j.Templates) > 0 {
		st.PromptSHA256 = PromptHash(j.Templates)
	}
	used := j.Used
	if len(used) == 0 {
		set := map[string]bool{}
		for _, h := range j.Templates {
			set[h] = true
		}
		used = sortedSet(set)
	}
	recs, err := ReadRecords(s.Root)
	if err != nil {
		return nil, err
	}
	st.Problems = append(st.Problems, recs.Problems...)
	st.Records = len(recs.Valid)
	as := assess(judgeGroup{id: j.ID, templates: used}, recs, th, false)
	st.State, st.Detail, st.Caps = as.state, as.detail, as.caps
	if as.rec == nil {
		st.Detail += fmt.Sprintf("; judged verdicts are capped at INCONCLUSIVE until `judge calibrate %s` runs", s.ID)
		if st.HumanLabels < th.MinLabels {
			st.Detail += fmt.Sprintf(" on at least %d human labels (%d so far)", th.MinLabels, st.HumanLabels)
		}
		if missing := uncovered(recs, j.ID, used); len(missing) > 0 {
			st.Detail += "; templates without human evidence in the newest record: " + shortList(missing)
		}
		return st, nil
	}
	st.Latest = Summary(as.rec)
	st.Reminders = append(st.Reminders, Reminders(as.rec, th, set.SHA256, nil, now)...)
	return st, nil
}

// uncovered lists the templates in used that the newest record measuring
// the judge configuration has no human evidence for.
func uncovered(recs *RecordSet, id JudgeID, used []string) []string {
	var newest *schemas.CalibrationRecord
	for i := len(recs.Valid) - 1; i >= 0 && newest == nil; i-- {
		if id.measured(&recs.Valid[i]) {
			newest = &recs.Valid[i]
		}
	}
	if newest == nil {
		return nil
	}
	var out []string
	for _, t := range used {
		if TemplateStats(newest, t) == nil {
			out = append(out, t)
		}
	}
	return out
}
