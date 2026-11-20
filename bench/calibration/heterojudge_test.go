package calibration

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Audit 1-C-12: a calibrated majority judge model must not bless runs graded
// by a minority judge model that has no calibration record.
func TestMinorityJudgeModelNeedsItsOwnRecord(t *testing.T) {
	runs := append(armRuns("A", 5, 5), armRuns("B", 8, 2)...)
	j := *runs[0].Grade.Judge
	j.Model = "judge-Y"
	g := *runs[0].Grade
	g.Judge = &j
	runs[0].Grade = &g
	an := analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: runs, Records: records(testRecord(30, 30, 30, 30, t0)), SetSHA256: "set1", Now: now})
	jc := an.JudgeCalibration
	if jc == nil || jc.State != schemas.CalibrationStateUncalibrated || !strings.Contains(jc.Detail, "judge-Y") {
		t.Fatalf("calibration %+v", jc)
	}
	if an.Verdict.Decision != schemas.DecisionInconclusive || jc.CappedFrom != schemas.DecisionImproved || !jc.Caps {
		t.Fatalf("decision %s capped_from %s", an.Verdict.Decision, jc.CappedFrom)
	}
	// A passing record for judge-Y too restores the calibrated verdict.
	y := testRecord(30, 30, 30, 30, t0)
	y.Model, y.RecordID = "judge-Y", y.RecordID+"-y"
	an = analysis(schemas.DecisionImproved, 0.3)
	Apply(an, Input{Manifest: judgedManifest(false), Runs: runs, Records: records(testRecord(30, 30, 30, 30, t0), y), SetSHA256: "set1", Now: now})
	if an.JudgeCalibration.State != schemas.CalibrationStateCalibrated || an.Verdict.Decision != schemas.DecisionImproved {
		t.Fatalf("both judges calibrated: %+v %s", an.JudgeCalibration, an.Verdict.Decision)
	}
}
