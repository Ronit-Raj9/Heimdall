package suite

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// copyAs copies the pack at src into benchRoot/id with suite.yaml's id,
// description and canary changed, as Add benchmark publishes a staged draft
// under the benchmark's own id.
func copyAs(t *testing.T, benchRoot, src, id, canary string) string {
	t.Helper()
	dst := filepath.Join(benchRoot, id)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if d.Name() == LockFile {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	editFileHW18(t, filepath.Join(dst, SuiteFile), "id: mini", "id: "+id)
	editFileHW18(t, filepath.Join(dst, SuiteFile), "description: One-task fixture pack for the suite package tests.", "description: Published copy.\ncanary: "+canary)
	return dst
}

func editFileHW18(t *testing.T, p, old, new string) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s has no %q", p, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lockOf(t *testing.T, benchRoot, id string) *schemas.SuiteLock {
	t.Helper()
	s, err := newLocalLoader(benchRoot).Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	l, err := LockWith(s, fixedNow, testFPOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// admitIn records an admission of m01 under l and returns the stored record.
func admitIn(t *testing.T, l *schemas.SuiteLock) *schemas.AdmissionRecord {
	t.Helper()
	rec := &schemas.AdmissionRecord{Admitted: true, At: fixedNow}
	if err := RecordAdmission(l, "m01", rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// A task checked in the staged draft (its own id, no canary) carries into
// the published benchmark (another id, description and canary): the carry
// fingerprint leaves out what does not change how the task is graded.
func TestCarryReadinessAcrossStagingAndPublish(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	copyAs(t, benchRoot, packRoot, "zz-test-csv", utils.NewCanary())
	rec := admitIn(t, lockOf(t, benchRoot, "mini"))
	if rec.CarryFingerprint == "" {
		t.Fatal("RecordAdmission stored no carry fingerprint")
	}
	pub := lockOf(t, benchRoot, "zz-test-csv")
	if AdmissionFingerprint(pub.Fingerprints, "m01") == rec.Fingerprint {
		t.Fatal("test setup: the published suite.yaml should change the full admission fingerprint")
	}
	if got := CarryFingerprint(pub.Fingerprints, "m01"); got != rec.CarryFingerprint {
		t.Fatalf("carry fingerprint differs across staging and publish: %s vs %s", got, rec.CarryFingerprint)
	}
}

// A changed check (suite verifier or the task's hidden tests) is never
// carried.
func TestCarryFingerprintChangesWithVerifier(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	rec := admitIn(t, lockOf(t, benchRoot, "mini"))

	a := copyAs(t, benchRoot, packRoot, "pub-a", utils.NewCanary())
	editFileHW18(t, filepath.Join(a, SuiteFile), "timeout_s: 60", "timeout_s: 90")
	if CarryFingerprint(lockOf(t, benchRoot, "pub-a").Fingerprints, "m01") == rec.CarryFingerprint {
		t.Fatal("a changed suite verifier carried")
	}

	b := copyAs(t, benchRoot, packRoot, "pub-b", utils.NewCanary())
	tests := filepath.Join(b, "tasks", "m01", "verifier", "tests", "test_hidden.py")
	raw, err := os.ReadFile(tests)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tests, append(raw, []byte("\n# changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if CarryFingerprint(lockOf(t, benchRoot, "pub-b").Fingerprints, "m01") == rec.CarryFingerprint {
		t.Fatal("a changed hidden test carried")
	}
}

// A held-out task stamped with the canary in the draft before readiness
// still carries after publish stamps the same canary (a no-op) and writes
// the canary into the published suite.yaml.
func TestCarryWithHeldOutTask(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	guid := utils.NewCanary()
	editFileHW18(t, filepath.Join(packRoot, "tasks", "m01", TaskFile), "kind: bugfix", "kind: bugfix\nsplit: held_out")
	editFileHW18(t, filepath.Join(packRoot, SuiteFile), "description: One-task fixture pack for the suite package tests.",
		"description: One-task fixture pack for the suite package tests.\ncanary: "+guid)
	draft, err := newLocalLoader(benchRoot).Load(context.Background(), "mini")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StampCanaryTasks(draft, guid, nil); err != nil {
		t.Fatal(err)
	}
	rec := admitIn(t, lockOf(t, benchRoot, "mini"))

	pubDir := copyAs(t, benchRoot, packRoot, "zz-held", guid)
	editFileHW18(t, filepath.Join(pubDir, SuiteFile), "canary: "+guid+"\ncanary: "+guid, "canary: "+guid)
	pub, err := newLocalLoader(benchRoot).Load(context.Background(), "zz-held")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StampCanaryTasks(pub, guid, []string{"m01"}); err != nil {
		t.Fatal(err)
	}
	if got := CarryFingerprint(lockOf(t, benchRoot, "zz-held").Fingerprints, "m01"); got != rec.CarryFingerprint {
		t.Fatal("a held-out task did not carry across publish")
	}
}
