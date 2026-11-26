package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/taskguard"
)

// TestAdmitManyAppliesGuard: a generated task whose instruction holds its
// answer is not admitted through the many-at-once path, even when the
// admission run itself let it through.
func TestAdmitManyAppliesGuard(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tasks", "t1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "instruction.md"), []byte("Answer the customer.\nTell them: the refund takes 5 business days.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := taskguard.Write(dir, taskguard.Record{Builder: taskguard.BuilderTrace, LeakTexts: []string{"The refund takes 5 business days"}}); err != nil {
		t.Fatal(err)
	}
	yes := true
	task := &schemas.Task{ID: "t1", Dir: dir, Instruction: "instruction.md",
		Provenance: &schemas.TaskProvenance{CreatedBy: "task from-trace",
			Source:              "trace a.jsonl (claude) sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			InstructionReviewed: &yes}}
	s := &schemas.Suite{ID: "s", Root: root, Tasks: []*schemas.Task{task}}
	admitted := func(context.Context, suite.AdmitDeps, *schemas.Suite, *schemas.Task) (*schemas.AdmissionRecord, error) {
		return &schemas.AdmissionRecord{Admitted: true, ReferenceResults: []string{"pass", "pass", "pass"}, Checks: []schemas.AdmissionCheck{
			{Name: "reference_1", Want: "pass", Got: "pass", Passed: true},
			{Name: "empty_patch", Want: "fail", Got: "fail", Passed: true},
			{Name: "mutant_1", Want: "fail", Got: "fail", Passed: true},
			{Name: "null_noop", Want: "fail", Got: "fail", Passed: true},
		}}, nil
	}
	rec, _ := admitOne(context.Background(), suite.AdmitDeps{}, s, task, admitted, map[string][]suite.Problem{"t1": nil})
	if rec.Admitted {
		t.Fatalf("leaking generated task admitted through admitOne: %+v", rec.Checks)
	}
	found := false
	for _, c := range rec.Checks {
		if c.Name == taskguard.CheckLeak {
			found = true
			if c.Passed {
				t.Fatalf("leak check passed: %+v", c)
			}
		}
	}
	if !found {
		t.Fatalf("no %s check in %+v", taskguard.CheckLeak, rec.Checks)
	}
}
