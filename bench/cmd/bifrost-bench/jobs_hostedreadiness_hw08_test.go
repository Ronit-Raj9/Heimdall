package main

import (
	"strings"
	"testing"
)

// Fix wave 3, N273: a reference answer the grader could not grade refuses
// the task with the grade's own reason, not only "invalid_evaluator".
func TestReadinessRefuseNamesGradeReason(t *testing.T) {
	s, tasks := readinessFixture(t)
	e := readinessExperiment("qa", bridgeOracleAgent, bridgeNoopAgent, s, tasks, "public", true)
	why := "judge: the judge model gave no answer within 16 reply tokens (it spent them on reasoning). Raise judge max_tokens in suite.yaml or pin a non-reasoning model."
	var runs []bridgeRun
	for arm := range e.Arms {
		r := bridgeRun{Task: tasks[0].ID, Arm: arm, Outcome: "fail", RunID: "r-" + arm}
		if isReferenceArm(arm) {
			r.Outcome, r.GradeReason = "invalid_evaluator", why
		}
		runs = append(runs, r)
	}
	got := readinessVerdict(s, tasks[:1], runs, map[string]string{tasks[0].ID: "e_1"}, nil)
	want := "the right answer (reference 1) could not be graded: " + why
	if got[0].Admitted || !strings.HasPrefix(got[0].Reason, want) {
		t.Fatalf("reason %q, want it to start with %q", got[0].Reason, want)
	}
}
