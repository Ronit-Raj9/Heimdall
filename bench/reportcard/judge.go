package reportcard

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// scenarioJudgeKey is the manifest scenario input a judged suite freezes
// (engine/scenarioplan.go, calibration.scenarioJudgeKey).
const scenarioJudgeKey = "suite.judge"

// JudgeStateUnchecked is a judged benchmark whose calibration could not be
// read (no calibration source on this server, or the suite is gone).
const JudgeStateUnchecked = "unchecked"

// JudgeState is the calibration state of a judge-graded benchmark on the
// report card (G4-06). Trusted is true for a judge calibrated on human labels,
// calibrated on its admission controls, or with too few human labels whose
// bounds already pass: verdicts and min_score gates may use it, with Label
// shown beside the score. Otherwise A/B verdicts on the benchmark are capped
// at unclear and a min_score gate on it is inconclusive.
type JudgeState struct {
	State        string `json:"state"` // calibration state (schemas.CalibrationState*), or unchecked
	Label        string `json:"label"` // "human-calibrated", "calibrated on controls", ...
	Trusted      bool   `json:"trusted"`
	Model        string `json:"model,omitempty"`
	HumanLabels  int    `json:"human_labels"`
	ControlItems int    `json:"control_items"`
	MinLabels    int    `json:"min_labels,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// JudgeStateFrom reads a suite's calibration status (GET
// /suites/{id}/calibration) as a report card judge state. A status that
// says the suite is not judged gives nil.
func JudgeStateFrom(st *schemas.CalibrationStatus) *JudgeState {
	if st == nil || !st.Judged {
		return nil
	}
	js := &JudgeState{State: st.State, Model: st.Model, HumanLabels: st.HumanLabels, ControlItems: st.SeedItems, MinLabels: st.Thresholds.MinLabels, Detail: st.Detail}
	if st.Latest != nil {
		js.ControlItems = st.Latest.SeedItems
	}
	switch st.State {
	case schemas.CalibrationStateCalibrated:
		js.Label, js.Trusted = "human-calibrated", true
	case schemas.CalibrationStateControls:
		js.Label, js.Trusted = "calibrated on controls", true
	case schemas.CalibrationStateFewLabels:
		js.Label, js.Trusted = fmt.Sprintf("few human labels (%d of %d)", st.HumanLabels, st.Thresholds.MinLabels), !st.Caps
	case schemas.CalibrationStateUnreliable:
		js.Label = "judge unreliable"
	default:
		js.Label = "judge not calibrated"
	}
	return js
}

// judgeState asks of for a judged benchmark's state; without an answer the
// judge counts as unchecked.
func judgeState(of func(string) *JudgeState, benchmark string) *JudgeState {
	id, _, _ := strings.Cut(benchmark, "@")
	if of != nil {
		if js := of(id); js != nil {
			return js
		}
	}
	return &JudgeState{State: JudgeStateUnchecked, Label: "judge calibration not checked"}
}

// capNote ends the summary of a verdict the judge's state took back.
func (j *JudgeState) capNote() string {
	if j == nil {
		return ""
	}
	return fmt.Sprintf("Graded by a model judge (%s), so this is not a decision: calibrate the judge on its admission controls or on human labels.", j.Label)
}
