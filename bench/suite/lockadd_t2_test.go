package suite

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestLockAddingTaskKeepsAdmission: adding a second task to the suite's
// tasks list keeps the unchanged admitted task admitted (PROOF_REPO.md side
// finding), and its record stays current; a real change still resets it.
func TestLockAddingTaskKeepsAdmission(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	lockMiniWith(t, benchRoot, packRoot, testFPOpts(nil))
	src, dst := filepath.Join(packRoot, "tasks", "m01"), filepath.Join(packRoot, "tasks", "m02")
	if b, err := exec.Command("cp", "-r", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, b)
	}
	editFile(t, filepath.Join(dst, "task.yaml"), "id: m01", "id: m02")
	editFile(t, filepath.Join(packRoot, SuiteFile), "tasks: [m01]", "tasks: [m01, m02]")
	s := loadMini(t, benchRoot)
	l, err := LockWith(s, fixedNow, testFPOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	tl := l.Tasks["m01"]
	if tl.Status != schemas.TaskAdmitted || tl.Admission == nil || !tl.Admission.Admitted {
		t.Fatalf("adding m02 reset m01: %+v", tl)
	}
	if !AdmissionCurrent(l, "m01", tl.Admission) {
		t.Fatal("carried admission is not current under the new lock")
	}
	if l.Tasks["m02"].Status != schemas.TaskDraft {
		t.Fatalf("new task m02 not draft: %+v", l.Tasks["m02"])
	}
	// A changed task file still resets it.
	if _, err := WriteLock(filepath.Join(packRoot, LockFile), l); err != nil {
		t.Fatal(err)
	}
	ins := filepath.Join(src, "instruction.md")
	b, err := os.ReadFile(ins)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ins, append(b, []byte("\nAlso this.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	l2, err := LockWith(loadMini(t, benchRoot), fixedNow, testFPOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if tl := l2.Tasks["m01"]; tl.Status != schemas.TaskDraft || tl.Admission != nil {
		t.Fatalf("a changed instruction kept the admission: %+v", tl)
	}
}
