package ci

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// TestRecordedBundleSeals verifies, for every experiment in the recorded
// bundle, the seals.log chain and the seal of every sealed run. It runs on a
// copy so the store never touches the checked-in files.
func TestRecordedBundleSeals(t *testing.T) {
	ctx := context.Background()
	st, root := openCopy(t)
	ids, err := st.ListExperiments(ctx)
	if err != nil {
		t.Fatalf("list experiments: %v", err)
	}
	if len(ids) == 0 {
		t.Fatalf("no experiment with a manifest.json under %s", bundleRoot())
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			if err := st.VerifyChain(id); err != nil {
				t.Fatalf("seals.log chain: %v", err)
			}
			runs := sealedRuns(t, root, id)
			if len(runs) == 0 {
				t.Fatalf("experiment %s has no sealed run", id)
			}
			for _, runID := range runs {
				if err := st.Verify(ctx, id, runID); err != nil {
					t.Errorf("verify %s: %v", runID, err)
				}
			}
		})
	}
}

// TestRecordedBundleTamper proves the guard bites: one changed, added or
// removed file in a sealed run must fail verification.
func TestRecordedBundleTamper(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		tamper func(t *testing.T, runDir string)
	}{
		{"changed file", func(t *testing.T, runDir string) {
			appendTo(t, filepath.Join(runDir, "grade.json"), "\n")
		}},
		{"added file", func(t *testing.T, runDir string) {
			appendTo(t, filepath.Join(runDir, "conftest.py"), "def pytest_configure(): pass\n")
		}},
		{"removed file", func(t *testing.T, runDir string) {
			if err := os.Remove(filepath.Join(runDir, "metrics.json")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, root := openCopy(t)
			ids, err := st.ListExperiments(ctx)
			if err != nil || len(ids) == 0 {
				t.Fatalf("list experiments: %v (%d found)", err, len(ids))
			}
			id := ids[0]
			runID := sealedRuns(t, root, id)[0]
			matches, err := filepath.Glob(filepath.Join(root, id, "slots", "*", "runs", runID))
			if err != nil || len(matches) != 1 {
				t.Fatalf("find run %s: %v (%d matches)", runID, err, len(matches))
			}
			tt.tamper(t, matches[0])
			if err := st.Verify(ctx, id, runID); !errors.Is(err, schemas.ErrSealMismatch) {
				t.Fatalf("verify after tamper: got %v, want ErrSealMismatch", err)
			}
		})
	}
}

// bundleRoot is the bundle under test: BENCH_CI_BUNDLE when set (the
// composite action sets it to the bundle it is about to replay), else
// RecordedRoot. A relative path resolves against the bench root.
func bundleRoot() string {
	root := os.Getenv("BENCH_CI_BUNDLE")
	if root == "" {
		root = RecordedRoot
	}
	if filepath.IsAbs(root) {
		return root
	}
	return filepath.Join("..", root)
}

// openCopy copies the recorded bundle to a temp folder and opens a store on
// it. The copy is writable, as a git checkout of the bundle is.
func openCopy(t *testing.T) (*store.FSStore, string) {
	t.Helper()
	src := bundleRoot()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
	st, err := store.NewFSStore(dst, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st, dst
}

// sealedRuns lists the run ids of an experiment that carry a seal.json.
func sealedRuns(t *testing.T, root, expID string) []string {
	t.Helper()
	seals, err := filepath.Glob(filepath.Join(root, expID, "slots", "*", "runs", "*", "seal.json"))
	if err != nil {
		t.Fatalf("glob seals: %v", err)
	}
	out := make([]string, 0, len(seals))
	for _, p := range seals {
		out = append(out, filepath.Base(filepath.Dir(p)))
	}
	return out
}

func appendTo(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", p, err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", p, err)
	}
}
