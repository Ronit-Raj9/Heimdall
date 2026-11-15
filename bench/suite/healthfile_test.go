package suite

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestHealthFileRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := &schemas.Suite{ID: "s", Root: root, Tasks: []*schemas.Task{{ID: "t01", Version: "1.2.0"}, {ID: "t02", Version: "1.0.0"}}}
	f, err := ReadHealthFile(root)
	if err != nil || len(f.Quarantined) != 0 {
		t.Fatalf("missing sidecar: %+v %v", f, err)
	}
	tests := []struct {
		name    string
		task    string
		reason  string
		wantErr string
	}{
		{"unknown task", "t09", "x", "task t09 is not in suite s"},
		{"empty reason", "t01", "  ", "a reason is required"},
		{"ok", "t01", "reference flips", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Quarantine(s, tt.task, schemas.TaskQuarantine{Reason: tt.reason})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	f, err = ReadHealthFile(root)
	if err != nil {
		t.Fatal(err)
	}
	q := f.Quarantined["t01"]
	if f.Suite != "s" || q.Reason != "reference flips" || q.Source != schemas.QuarantineManual || q.TaskVersion != "1.2.0" || q.At.IsZero() {
		t.Fatalf("record %+v", f)
	}
	if ok, err := Restore(s, "t02"); ok || err != nil {
		t.Fatalf("restore of a task never quarantined: %t %v", ok, err)
	}
	if ok, err := Restore(s, "t01"); !ok || err != nil {
		t.Fatalf("restore: %t %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(root, schemas.SuiteHealthFileName)); !os.IsNotExist(err) {
		t.Fatalf("an empty quarantine must remove the sidecar: %v", err)
	}
}

func TestReadHealthFileBroken(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, schemas.SuiteHealthFileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHealthFile(root); err == nil {
		t.Fatal("broken sidecar accepted")
	}
}

func TestTaskSplit(t *testing.T) {
	if TaskSplit(nil) != schemas.SplitPublic || TaskSplit(&schemas.Task{}) != schemas.SplitPublic ||
		TaskSplit(&schemas.Task{Split: schemas.SplitHeldOut}) != schemas.SplitHeldOut {
		t.Fatal("TaskSplit")
	}
}

// TestQuarantineKeepsCommittedLocks quarantines a task of every committed
// suite (in a temp copy) and checks the sidecar never reaches the lock:
// suite.lock is untouched on disk, re-locking gives the same bytes as before
// the quarantine, and a pack whose lock matches its files re-locks
// byte-identical to the committed suite.lock.
func TestQuarantineKeepsCommittedLocks(t *testing.T) {
	locks, err := filepath.Glob(filepath.Join("..", "benchmarks", "*", "suite.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) == 0 {
		t.Skip("no committed suite locks")
	}
	for _, lockPath := range locks {
		id := filepath.Base(filepath.Dir(lockPath))
		t.Run(id, func(t *testing.T) {
			benchRoot := t.TempDir()
			dir := filepath.Join(benchRoot, id)
			if err := copyTree(filepath.Dir(lockPath), dir); err != nil {
				t.Fatal(err)
			}
			committed, err := os.ReadFile(filepath.Join(dir, "suite.lock"))
			if err != nil {
				t.Fatal(err)
			}
			s, err := newLocalLoader(benchRoot).Load(context.Background(), id)
			if err != nil {
				t.Skipf("suite does not load: %v", err)
			}
			before := relockBytes(t, s)
			if err := Quarantine(s, s.Tasks[0].ID, schemas.TaskQuarantine{Reason: "test", At: time.Unix(0, 0).UTC()}); err != nil {
				t.Fatal(err)
			}
			onDisk, err := os.ReadFile(filepath.Join(dir, "suite.lock"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(onDisk, committed) {
				t.Fatal("quarantine wrote to suite.lock")
			}
			s2, err := newLocalLoader(benchRoot).Load(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if s2.LockHash != s.LockHash {
				t.Fatalf("lock hash changed: %s -> %s", s.LockHash, s2.LockHash)
			}
			after := relockBytes(t, s2)
			if !bytes.Equal(before, after) {
				t.Fatal("re-lock differs after a quarantine")
			}
			if !bytes.Equal(before, committed) {
				// A pack still being authored (or imported) has a lock older
				// than its files; that is not the sidecar's doing, and the
				// before/after check above already covers it.
				t.Logf("%s: suite.lock is stale against its files; checked before/after only", id)
				return
			}
			if !bytes.Equal(after, committed) {
				t.Fatalf("%s does not re-lock byte-identical to its committed suite.lock with a task quarantined", id)
			}
		})
	}
}

// relockBytes is what `suite lock` would write for s, without writing it
// over the committed file.
func relockBytes(t *testing.T, s *schemas.Suite) []byte {
	t.Helper()
	l, err := Lock(s, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	b, err := WriteLock(filepath.Join(t.TempDir(), "suite.lock"), l)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
