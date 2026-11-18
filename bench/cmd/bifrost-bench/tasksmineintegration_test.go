//go:build integration

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/quickstart"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestIntegrationTasksMine is `init` then `tasks mine` end to end with the
// real environment and grader on bench/coding-core-base:1: the fix commit of
// quickRepo and a feature commit are proved (both admitted in one
// suite.lock, with every check),
// and a commit whose new test already passes at its parent is dropped, its
// folder removed and suite.yaml restored. Nothing calls a model and the
// repository is not changed.
func TestIntegrationTasksMine(t *testing.T) {
	if out, err := exec.Command("docker", "image", "inspect", quickstart.HelperImage).CombinedOutput(); err != nil {
		t.Skipf("%s is not available locally: %s", quickstart.HelperImage, out)
	}
	dir, _, ref := quickRepo(t)
	// A second provable commit: a new function with its test.
	if err := os.WriteFile(filepath.Join(dir, "calc", "mul.py"), []byte("def mul(a, b):\n    return a * b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "test_mul.py"), []byte("from calc.mul import mul\n\n\ndef test_mul():\n    assert mul(2, 3) == 6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, dir, "add", "-A")
	gitFixture(t, dir, "commit", "-q", "-m", "Add mul")
	second := gitFixture(t, dir, "rev-parse", "HEAD")
	// An unproven commit: a docstring and a test of behaviour that already worked.
	p := filepath.Join(dir, "calc", "core.py")
	if err := os.WriteFile(p, []byte("def add(a, b):\n    \"\"\"Sum.\"\"\"\n    return a + b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "test_zero.py"), []byte("from calc.core import add\n\n\ndef test_zero():\n    assert add(0, 0) == 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, dir, "add", "-A")
	gitFixture(t, dir, "commit", "-q", "-m", "Document add")
	unproven := gitFixture(t, dir, "rev-parse", "HEAD")
	head := unproven

	old := detectProbe
	detectProbe = fakeMachine
	t.Cleanup(func() { detectProbe = old })
	t.Chdir(dir)
	t.Setenv("BENCH_RUNS_ROOT", t.TempDir())
	if code := runInit(nil); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	if code := runTasks([]string{"mine", "-limit", "5"}); code != exitOK {
		t.Fatalf("tasks mine exit %d", code)
	}
	suiteDir := filepath.Join(dir, ".heimdall", "benchmarks", "lib")
	provedID, droppedID := "m-"+ref[:10], "m-"+unproven[:10]
	if _, err := os.Stat(filepath.Join(suiteDir, "tasks", provedID, "task.yaml")); err != nil {
		t.Fatalf("proven task missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(suiteDir, "tasks", droppedID)); err == nil {
		t.Errorf("unproven task %s was kept", droppedID)
	}
	suiteYAML, _ := os.ReadFile(filepath.Join(suiteDir, "suite.yaml"))
	if !strings.Contains(string(suiteYAML), provedID) || strings.Contains(string(suiteYAML), droppedID) {
		t.Errorf("suite.yaml tasks:\n%s", suiteYAML)
	}
	// Both proofs survive the single lock write (adding the second id to
	// suite.yaml must not clear the first admission).
	assertAdmittedID(t, filepath.Join(suiteDir, "suite.lock"), provedID)
	assertAdmittedID(t, filepath.Join(suiteDir, "suite.lock"), "m-"+second[:10])
	instr, _ := os.ReadFile(filepath.Join(suiteDir, "tasks", provedID, "instruction.md"))
	if strings.Contains(string(instr), "a + b") || !strings.Contains(string(instr), "sign") {
		t.Errorf("instruction:\n%s", instr)
	}
	if got := gitFixture(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved: %s", got)
	}
	if st := gitFixture(t, dir, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Errorf("tasks mine changed tracked files: %s", st)
	}
}

// assertAdmittedID reads suite.lock and checks that task id is admitted
// with the determinism repeats and the control checks.
func assertAdmittedID(t *testing.T, path, id string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lock schemas.SuiteLock
	if err := json.Unmarshal(b, &lock); err != nil {
		t.Fatal(err)
	}
	tl := lock.Tasks[id]
	if tl.Status != schemas.TaskAdmitted || tl.Admission == nil || !tl.Admission.Admitted || tl.Admission.ReferenceRepeats != 3 {
		t.Fatalf("%s not admitted: %+v", id, tl)
	}
	seen := map[string]bool{}
	for _, c := range tl.Admission.Checks {
		seen[c.Name] = true
	}
	for _, want := range []string{"static_validation", "empty_patch", "exploit", "null_noop", "null_refuse", "instruction_edited", "secret_scan", "verifier_no_network"} {
		if !seen[want] {
			t.Errorf("admission lacks check %s", want)
		}
	}
}
