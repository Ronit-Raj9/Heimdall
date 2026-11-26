package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestGuardCheckReasonServed: a task quality guard check keeps its plain
// reason in the served summary; other checks still keep grader counts only.
func TestGuardCheckReasonServed(t *testing.T) {
	task := &schemas.Task{ID: "t1", Dir: t.TempDir()}
	s := &schemas.Suite{ID: "s", Lock: &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{"t1": {
		Status: schemas.TaskDraft,
		Admission: &schemas.AdmissionRecord{Checks: []schemas.AdmissionCheck{
			{Name: "guard_no_answer_leak", Passed: false, Detail: "The instruction or the workspace holds the answer: instruction.md, line 2."},
			{Name: "mutant_1", Passed: true, Detail: "controls/m1.diff; tests_ran=3 failures=1"},
		}},
	}}}}
	got := summarizeTask(s, task, nil)
	if len(got.Checks) != 2 {
		t.Fatalf("checks = %+v", got.Checks)
	}
	if got.Checks[0].Summary != "The instruction or the workspace holds the answer: instruction.md, line 2." {
		t.Fatalf("guard summary = %q", got.Checks[0].Summary)
	}
	if got.Checks[1].Summary != "tests_ran=3 failures=1" {
		t.Fatalf("mutant summary = %q", got.Checks[1].Summary)
	}
}
