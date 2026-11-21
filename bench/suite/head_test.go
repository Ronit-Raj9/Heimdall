package suite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// writeHeadSuite writes a pack of n tasks whose task.yaml files are not
// YAML: only a loader that opens them fails.
func writeHeadSuite(t *testing.T, root, id string, n, admitted int) string {
	t.Helper()
	dir := filepath.Join(root, id)
	ids := make([]string, n)
	lock := schemas.SuiteLock{Suite: id, Tasks: map[string]schemas.TaskLock{}}
	for i := range ids {
		ids[i] = fmt.Sprintf("t%05d", i)
		td := filepath.Join(dir, "tasks", ids[i])
		if err := os.MkdirAll(td, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(td, TaskFile), []byte("{{ not yaml"), 0o644); err != nil {
			t.Fatal(err)
		}
		tl := schemas.TaskLock{Status: schemas.TaskNotLocked}
		if i < admitted {
			tl = schemas.TaskLock{Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{Admitted: true}}
		}
		lock.Tasks[ids[i]] = tl
	}
	yaml := "id: " + id + "\nversion: 1.0.0\nenvironment: docker_workspace\ngrader: verifier_command\n" +
		"base_image:\n  tag: x:1\nworkdir: /workspace\nverifier:\n  command: [\"true\"]\n  timeout_s: 60\n  result: exit_code\n" +
		"tasks: [" + strings.Join(ids, ", ") + "]\nprobes: []\n"
	if err := os.WriteFile(filepath.Join(dir, SuiteFile), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, LockFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// LoadHead of a 5,000-task pack reads suite.yaml and suite.lock only: every
// task.yaml is garbage, so opening any one of them would fail the load.
func TestLoadHeadReadsNoTaskFile(t *testing.T) {
	root := t.TempDir()
	dir := writeHeadSuite(t, root, "big", 5000, 1200)
	start := time.Now()
	s, err := LoadHead(dir)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("LoadHead: %v", err)
	}
	if len(s.TaskIDs) != 5000 || len(s.Tasks) != 0 || s.Lock == nil || s.LockHash == "" {
		t.Fatalf("head: %d ids, %d tasks, lock %v", len(s.TaskIDs), len(s.Tasks), s.Lock != nil)
	}
	n := 0
	for _, tl := range s.Lock.Tasks {
		if tl.Status == schemas.TaskAdmitted {
			n++
		}
	}
	if n != 1200 {
		t.Fatalf("admitted in lock: %d", n)
	}
	t.Logf("LoadHead of 5000 tasks: %s", took)
	if took > 5*time.Second {
		t.Errorf("LoadHead took %s", took)
	}
	// The full load does open the task files, and fails on them.
	if _, err := newLocalLoader(root).Load(context.Background(), "big"); err == nil {
		t.Fatal("full load of garbage task files succeeded")
	}
}

// A pack whose suite.yaml lists no tasks is counted by its task folders.
func TestLoadHeadCountsTaskFolders(t *testing.T) {
	dir := writeHeadSuite(t, t.TempDir(), "p", 3, 0)
	raw, _ := os.ReadFile(filepath.Join(dir, SuiteFile))
	out := strings.Replace(string(raw), "tasks: [t00000, t00001, t00002]", "tasks: []", 1)
	if err := os.WriteFile(filepath.Join(dir, SuiteFile), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadHead(dir)
	if err != nil || len(s.TaskIDs) != 3 {
		t.Fatalf("LoadHead: %v, ids %v", err, s)
	}
}

// Stamp changes with the modification time of suite.yaml, suite.lock, the
// quarantine sidecar and the tasks folder; perTask also sees task.yaml.
func TestStampChangesOnMtime(t *testing.T) {
	dir := writeHeadSuite(t, t.TempDir(), "p", 2, 0)
	later := time.Now().Add(time.Hour)
	for _, rel := range []string{SuiteFile, LockFile, "tasks"} {
		before := Stamp(dir, false)
		if Stamp(dir, false) != before {
			t.Fatal("stamp not stable")
		}
		later = later.Add(time.Minute)
		if err := os.Chtimes(filepath.Join(dir, rel), later, later); err != nil {
			t.Fatal(err)
		}
		if Stamp(dir, false) == before {
			t.Errorf("stamp unchanged after touching %s", rel)
		}
	}
	before := Stamp(dir, false)
	if err := os.WriteFile(filepath.Join(dir, schemas.SuiteHealthFileName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Stamp(dir, false) == before {
		t.Error("stamp unchanged after writing the quarantine sidecar")
	}
	task := filepath.Join(dir, "tasks", "t00001", TaskFile)
	lib, org := Stamp(dir, false), Stamp(dir, true)
	later = later.Add(time.Minute)
	if err := os.Chtimes(task, later, later); err != nil {
		t.Fatal(err)
	}
	if Stamp(dir, false) != lib {
		t.Error("library stamp changed on a task.yaml edit")
	}
	if Stamp(dir, true) == org {
		t.Error("org stamp unchanged on a task.yaml edit")
	}
}
