package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// codingCore copies coding-core into a fresh benchmarks folder.
func codingCore(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "benchmarks", "coding-core")
	if _, err := os.Stat(filepath.Join(src, "suite.yaml")); err != nil {
		t.Skipf("coding-core not readable: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "coding-core"), os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSuiteLintAndLock(t *testing.T) {
	root := codingCore(t)
	ctx := context.Background()
	var out bytes.Buffer
	rep, err := SuiteLint(ctx, root, "coding-core", &out)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Suite != "coding-core" || rep.Errors != 0 || rep.Tasks == 0 || !strings.Contains(out.String(), "coding-core@") {
		t.Fatalf("lint %+v\n%s", rep, out.String())
	}
	if _, err := SuiteLint(ctx, root, "missing", nil); err == nil || !strings.HasPrefix(err.Error(), "load suite missing: ") {
		t.Fatalf("missing suite: %v", err)
	}
	out.Reset()
	lock, err := SuiteLock(ctx, LockOptions{BenchRoot: root, Ref: "coding-core"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(lock.Path)
	if err != nil {
		t.Fatal(err)
	}
	if lock.LockHash != utils.SHA256Bytes(raw) || lock.Tasks != rep.Tasks || !strings.Contains(out.String(), "wrote "+lock.Path) {
		t.Fatalf("lock %+v\n%s", lock, out.String())
	}
}

func TestValidIDAndConfine(t *testing.T) {
	for _, ok := range []string{"coding-core", "t01", "e_123", "rg_ab.c"} {
		if err := ValidID("id", ok); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", "../x", "a..b", "-x", strings.Repeat("a", 129), "a b"} {
		if err := ValidID("id", bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "in.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "x.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, err := Confine(root, "agent", "in.yaml"); err != nil || filepath.Base(p) != "in.yaml" {
		t.Fatalf("inside: %q %v", p, err)
	}
	for _, bad := range []string{"", "../x.yaml", "/etc/passwd", "out/x.yaml", "missing.yaml"} {
		if _, err := Confine(root, "agent", bad); err == nil {
			t.Fatalf("%q confined", bad)
		}
	}
}

func TestCheckSuiteDriftNeedsFrozenLock(t *testing.T) {
	s := &schemas.Suite{ID: "s", LockHash: "sha256:new"}
	m := &schemas.Manifest{SuiteLockHash: "sha256:old"}
	if err := CheckSuiteDrift(m, s, ""); err == nil || !strings.Contains(err.Error(), "-suite-lock") {
		t.Fatalf("drift without a frozen lock: %v", err)
	}
	if err := CheckSuiteDrift(&schemas.Manifest{SuiteLockHash: "sha256:new"}, s, ""); err != nil {
		t.Fatalf("same lock: %v", err)
	}
}

// TestCheckEvaluatorRefusesEditedTask is the regression test for audit
// finding 8-D-01: with suite.lock unchanged, a task edited after locking
// (here a new protected path) changes what G1 reads, so a regate refuses
// instead of applying the edited rule to the sealed diffs. A lock without
// fingerprints is refused too: it binds nothing to compare.
func TestCheckEvaluatorRefusesEditedTask(t *testing.T) {
	root := codingCore(t)
	ctx := context.Background()
	if _, err := SuiteLock(ctx, LockOptions{BenchRoot: root, Ref: "coding-core"}, nil); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSuite(ctx, root, "coding-core")
	if err != nil {
		t.Fatal(err)
	}
	// The registry the lock binds part versions from (LockFingerprintOptions).
	reg := &schemas.Registry{
		Environments: environments.Builtin(environments.DockerWorkspaceConfig{}),
		Graders:      append([]schemas.Grader{graders.NewJudge(graders.JudgeConfig{})}, graders.Builtin(graders.HiddenTestsConfig{})...),
	}
	m := &schemas.Manifest{SuiteLockHash: s.LockHash, Experiment: schemas.Experiment{Suite: "coding-core"},
		Slots: []schemas.Slot{{SlotID: "s_1", Task: "t01", Condition: schemas.ConditionClean}}}
	if err := CheckSuiteDrift(m, s, ""); err != nil {
		t.Fatalf("lock drift: %v", err)
	}
	if err := CheckEvaluator(ctx, reg, m, s); err != nil {
		t.Fatalf("unchanged suite refused: %v", err)
	}
	p := filepath.Join(root, "coding-core", "tasks", "t01", "task.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, []byte("\n# edited after locking\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = LoadSuite(ctx, root, "coding-core")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSuiteDrift(m, s, ""); err != nil {
		t.Fatalf("the lock file did not change, yet: %v", err)
	}
	if err := CheckEvaluator(ctx, reg, m, s); err == nil || !strings.Contains(err.Error(), "task t01") {
		t.Fatalf("edited task t01 accepted: %v", err)
	}
	s.Lock.Fingerprints = nil
	if err := CheckEvaluator(ctx, reg, m, s); err == nil || !strings.Contains(err.Error(), "no fingerprints") {
		t.Fatalf("legacy lock accepted: %v", err)
	}
}

// TestCheckSuiteDriftIgnoresAdmission is the regression test for audit
// finding 8-D-02: a frozen lock whose used task differs only in admission
// and lifecycle metadata still regrades; a changed content hash does not.
func TestCheckSuiteDriftIgnoresAdmission(t *testing.T) {
	task := schemas.TaskLock{Version: "1", InstructionSHA256: "i", RepoTreeSHA256: "r", VerifierSHA256: "v",
		ControlsSHA256: "c", FixturesSHA256: "f", Status: schemas.TaskAdmitted}
	frozen := schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{"t01": task}}
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "suite.lock")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	now := task
	now.Status = schemas.TaskQuarantined
	now.Admission = &schemas.AdmissionRecord{Admitted: true, Fingerprint: "sha256:x"}
	s := &schemas.Suite{ID: "s", LockHash: "sha256:new", Lock: &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{"t01": now}}}
	m := &schemas.Manifest{SuiteLockHash: utils.SHA256Bytes(raw),
		Slots: []schemas.Slot{{SlotID: "s_1", Task: "t01", Condition: schemas.ConditionClean}}}
	if err := CheckSuiteDrift(m, s, p); err != nil {
		t.Fatalf("admission-only change refused: %v", err)
	}
	now.VerifierSHA256 = "v2"
	s.Lock.Tasks["t01"] = now
	if err := CheckSuiteDrift(m, s, p); err == nil || !strings.Contains(err.Error(), "task t01 changed") {
		t.Fatalf("changed verifier accepted: %v", err)
	}
}
