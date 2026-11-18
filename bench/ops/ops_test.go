package ops

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
