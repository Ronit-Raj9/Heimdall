package handlers

import (
	"context"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/reportcard"
)

// suiteJudgeState is the report card's judge state of benchmark id (G4-06):
// the suite's calibration status, as GET /suites/{id}/calibration reads it,
// at the default thresholds. nil when the suite cannot be loaded or is not
// judged (the report card then shows the judge as not checked).
func suiteJudgeState(ctx context.Context, suites SuiteSource, id string) *reportcard.JudgeState {
	if suites == nil {
		return nil
	}
	s, err := suites.LoadSuite(ctx, id)
	if err != nil || s == nil || s.Root == "" {
		return nil
	}
	st, err := calibration.Status(s, graders.StatusJudge(s), calibration.Thresholds(nil, ""), time.Now().UTC())
	if err != nil {
		return nil
	}
	return reportcard.JudgeStateFrom(st)
}
