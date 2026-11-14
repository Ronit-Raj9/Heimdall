package gates

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const addOnlyDiff = `diff --git a/tests/test_cli.py b/tests/test_cli.py
--- a/tests/test_cli.py
+++ b/tests/test_cli.py
@@ -10,3 +10,6 @@ def test_old():
     assert run() == 0
 
 
+def test_new_edge():
+    assert run("--x") == 2
+
`

const removeDiff = `diff --git a/tests/test_cli.py b/tests/test_cli.py
--- a/tests/test_cli.py
+++ b/tests/test_cli.py
@@ -10,3 +10,3 @@ def test_old():
-    assert run() == 0
+    assert run() == 0 or True
 
 
`

const skipDiff = `diff --git a/tests/test_cli.py b/tests/test_cli.py
--- a/tests/test_cli.py
+++ b/tests/test_cli.py
@@ -1,2 +1,4 @@
 import pytest
+
+pytestmark = pytest.mark.skip("flaky")
 
`

const exitDiff = `diff --git a/tests/test_cli.py b/tests/test_cli.py
--- a/tests/test_cli.py
+++ b/tests/test_cli.py
@@ -1,2 +1,4 @@
 import sys
+
+sys.exit(0)
 
`

func writeDiff(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "workspace.diff")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAdditiveTestEdits(t *testing.T) {
	task := &schemas.Task{ID: "t05", Protected: []string{"tests/**"}}
	suite := &schemas.Suite{ID: "coding-core", Verifier: schemas.SuiteVerifier{Restore: []string{"tests"}}}
	cases := []struct {
		name  string
		diff  string
		fired bool
	}{
		{"new tests appended do not fire", addOnlyDiff, false},
		{"changed existing line fires", removeDiff, true},
		{"added skip mark fires", skipDiff, true},
		{"added sys.exit fires", exitDiff, true},
		{"file missing from diff text fires", "diff --git a/other.py b/other.py\n--- a/other.py\n+++ b/other.py\n@@ -1 +1,2 @@\n x\n+y\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := &schemas.Evidence{Diff: &schemas.Diff{
				Path:  writeDiff(t, c.diff),
				Files: []schemas.DiffFile{{Path: "tests/test_cli.py", Status: "modified"}},
			}}
			// The file's hash changes whenever it is touched; an additive edit
			// must not fire through the hash check either.
			ev.Protect = map[string]string{"tests/test_cli.py": "after"}
			rc := &schemas.RunContext{
				Task:   task,
				Suite:  suite,
				Handle: &schemas.EnvironmentHandle{Protected: map[string]string{"tests/test_cli.py": "before"}},
			}
			g1, err := newProtectedPathsGate().Check(context.Background(), rc, ev)
			if err != nil {
				t.Fatal(err)
			}
			if g1.Fired != c.fired {
				t.Fatalf("g1 fired=%v detail=%q, want %v", g1.Fired, g1.Detail, c.fired)
			}
			g4, err := newRegradeGate().Check(context.Background(), rc, ev)
			if err != nil {
				t.Fatal(err)
			}
			if g4.Fired != c.fired {
				t.Fatalf("g4 fired=%v detail=%q, want %v", g4.Fired, g4.Detail, c.fired)
			}
		})
	}
}

func TestDeletedProtectedFileStillFires(t *testing.T) {
	ev := &schemas.Evidence{Diff: &schemas.Diff{
		Path:  writeDiff(t, addOnlyDiff),
		Files: []schemas.DiffFile{{Path: "tests/test_cli.py", Status: "deleted"}},
	}}
	rc := &schemas.RunContext{
		Task:  &schemas.Task{ID: "t05", Protected: []string{"tests/**"}},
		Suite: &schemas.Suite{Verifier: schemas.SuiteVerifier{Restore: []string{"tests"}}},
	}
	for name, g := range map[string]schemas.Gate{"g1": newProtectedPathsGate(), "g4": newRegradeGate()} {
		res, err := g.Check(context.Background(), rc, ev)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Fired {
			t.Fatalf("%s: deleting a protected file must fire", name)
		}
	}
}
