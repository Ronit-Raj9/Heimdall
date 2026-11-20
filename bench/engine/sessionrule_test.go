package engine

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestLastSessionGradedLikeSingleRun is the 2-B-17 regression: one rule for
// crash and timeout. A session before the last ends the run with its
// outcome; the last session, crashed or timed out, is graded as a
// single-session run is (docs/scenarios-design.md section 5).
func TestLastSessionGradedLikeSingleRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sessions []schemas.SessionResult
		exec     schemas.ExecResult
		passed   bool
		want     schemas.Outcome
	}{
		{"earlier crash ends", []schemas.SessionResult{{Index: 1, ExitCode: 1}}, schemas.ExecResult{ExitCode: 1}, true, schemas.OutcomeCrash},
		{"earlier timeout ends", []schemas.SessionResult{{Index: 1, TimedOut: true}}, schemas.ExecResult{TimedOut: true}, true, schemas.OutcomeTimeout},
		{"last crash, pass", []schemas.SessionResult{{Index: 1}, {Index: 2, ExitCode: 1}}, schemas.ExecResult{ExitCode: 1}, true, schemas.OutcomePass},
		{"last timeout, pass", []schemas.SessionResult{{Index: 1}, {Index: 2, TimedOut: true}}, schemas.ExecResult{TimedOut: true}, true, schemas.OutcomePass},
		{"last timeout, fail", []schemas.SessionResult{{Index: 1}, {Index: 2, TimedOut: true}}, schemas.ExecResult{TimedOut: true}, false, schemas.OutcomeTimeout},
		{"last crash, fail", []schemas.SessionResult{{Index: 1}, {Index: 2, ExitCode: 1}}, schemas.ExecResult{ExitCode: 1}, false, schemas.OutcomeFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ended := sessionsEnded(tc.sessions, 2)
			got := outcomeFrom(false, false, ended, nil, &schemas.Grade{Passed: tc.passed}, &tc.exec, utils.UpstreamTally{})
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
