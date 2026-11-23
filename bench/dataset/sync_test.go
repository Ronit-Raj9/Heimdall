package dataset

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func writeSyncTask(t *testing.T, dir, id, title, instruction string) {
	t.Helper()
	td := filepath.Join(dir, "tasks", id)
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, "task.yaml"), []byte("id: "+id+"\ntitle: "+title+"\ninstruction: instruction.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, "instruction.md"), []byte(instruction), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSyncSuite(t *testing.T, dir string, ids ...string) {
	t.Helper()
	body := "id: s1\nversion: \"1\"\ntasks:\n"
	for _, id := range ids {
		body += "  - " + id + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncSuiteRecordsAddedUpdatedRemoved(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "benchmarks", "s1")
	writeSyncTask(t, dir, "a", "Task A", "Do a.")
	writeSyncTask(t, dir, "b", "Task B", "Do b.")
	writeSyncSuite(t, dir, "a", "b")
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	v, err := SyncSuite(root, "s1", dir, "alice", "Rows import", now)
	if err != nil {
		t.Fatal(err)
	}
	if v == nil || v.N != 1 || v.Source != "Rows import" || v.Actor != "alice" || !slices.Equal(v.Added, []string{"a", "b"}) || v.Tasks != 2 {
		t.Fatalf("first sync: %+v", v)
	}
	snap, err := Open(root).Snapshot("s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(root).Row("s1", snap.Tasks["a"])
	if err != nil || r.Title != "Task A" || r.Input != "Do a." {
		t.Fatalf("stored row of a: %+v %v", r, err)
	}

	// b changes, a goes, c comes.
	if err := os.WriteFile(filepath.Join(dir, "tasks", "b", "instruction.md"), []byte("Do b better."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "tasks", "a")); err != nil {
		t.Fatal(err)
	}
	writeSyncTask(t, dir, "c", "Task C", "Do c.")
	writeSyncSuite(t, dir, "b", "c")
	v, err = SyncSuite(root, "s1", dir, "bob", "File editor", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if v == nil || v.N != 2 || v.Source != "File editor" || !slices.Equal(v.Added, []string{"c"}) ||
		!slices.Equal(v.Updated, []string{"b"}) || !slices.Equal(v.Removed, []string{"a"}) || v.Tasks != 2 {
		t.Fatalf("second sync: %+v", v)
	}
}

func TestSyncSuiteNoChangeNoVersion(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "benchmarks", "s1")
	writeSyncTask(t, dir, "a", "Task A", "Do a.")
	writeSyncSuite(t, dir, "a")
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	if _, err := SyncSuite(root, "s1", dir, "alice", "Rows import", now); err != nil {
		t.Fatal(err)
	}
	v, err := SyncSuite(root, "s1", dir, "alice", "Rows import", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if v != nil {
		t.Fatalf("no change recorded a version: %+v", v)
	}
	vs, _ := Open(root).Versions("s1")
	if len(vs) != 1 {
		t.Fatalf("versions = %d, want 1", len(vs))
	}
}

// A task the dataset API wrote (a full row without a folder hash) is not
// reported as changed by the next sync when its folder did not change.
func TestSyncSuiteKeepsDatasetAPIRows(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "benchmarks", "s1")
	writeSyncTask(t, dir, "a", "Task A", "Do a.")
	writeSyncSuite(t, dir, "a")
	store := Open(root)
	sha, data, _ := RowSHA(Row{ID: "a", Input: "Do a.", Expected: Strings{"4"}})
	if err := store.PutRow("s1", sha, data); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	if _, err := store.Record("s1", map[string]string{"a": sha}, []string{"a"}, nil, nil, "alice", "dataset API", now); err != nil {
		t.Fatal(err)
	}
	writeSyncTask(t, dir, "b", "Task B", "Do b.")
	writeSyncSuite(t, dir, "a", "b")
	v, err := SyncSuite(root, "s1", dir, "alice", "File editor", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if v == nil || !slices.Equal(v.Added, []string{"b"}) || len(v.Updated) != 0 || len(v.Removed) != 0 {
		t.Fatalf("sync after a dataset API row: %+v", v)
	}

	// The dataset API rewrites a: its own version holds the change, so the
	// next sync does not report a again.
	writeSyncTask(t, dir, "a", "Task A", "Do a, version two.")
	sha2, data2, _ := RowSHA(Row{ID: "a", Input: "Do a, version two.", Expected: Strings{"4"}})
	if err := store.PutRow("s1", sha2, data2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Record("s1", map[string]string{"a": sha2}, nil, []string{"a"}, nil, "alice", "dataset API", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if v, err := SyncSuite(root, "s1", dir, "alice", "File editor", now.Add(3*time.Minute)); err != nil || v != nil {
		t.Fatalf("sync after a dataset API update recorded %+v %v", v, err)
	}
}
