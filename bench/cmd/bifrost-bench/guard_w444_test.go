package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskguard"
)

// TestAdmissionRecordOfAppliesGuard: the hosted readiness record of a
// generated task carries the guard checks, and a leak keeps it out.
func TestAdmissionRecordOfAppliesGuard(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "instruction.md"), []byte("Say the refund takes 5 business days.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := taskguard.Write(dir, taskguard.Record{Builder: taskguard.BuilderAPI, LeakTexts: []string{"the refund takes 5 business days"}}); err != nil {
		t.Fatal(err)
	}
	task := &schemas.Task{ID: "g1", Dir: dir, Instruction: "instruction.md",
		Provenance: &schemas.TaskProvenance{CreatedBy: "api policy generator", Source: "draft d1"}}
	s := &schemas.Suite{ID: "s", Root: t.TempDir(), Tasks: []*schemas.Task{task}}
	checks := []schemas.AdmissionCheck{
		{Name: "reference_1", Passed: true}, {Name: "null_noop", Passed: true}, {Name: "mutant_1", Passed: true},
	}
	rec := admissionRecordOf(s, hostedAdmitTask{Task: "g1", Admitted: true, Checks: checks, ReferenceResults: []string{"pass"}})
	if rec.Admitted {
		t.Fatalf("leaking generated task admitted: %+v", rec.Checks)
	}
	found := false
	for _, c := range rec.Checks {
		if c.Name == taskguard.CheckLeak {
			found = !c.Passed
		}
	}
	if !found {
		t.Fatalf("no failed %s check in %+v", taskguard.CheckLeak, rec.Checks)
	}
	if taskByID(nil, "x") != nil || taskByID(s, "nope") != nil {
		t.Fatal("taskByID found a task that is not there")
	}
}
