package validate

import (
	"strings"
	"testing"
)

// marshmallow #2936's fix had a CHANGELOG hunk; the ablation without it
// passed the tests, so readiness refused the task as "near miss passed"
// (real hosted run). Documentation hunks are never ablated.
func TestHunkAblationsSkipDocs(t *testing.T) {
	patch := "diff --git a/CHANGELOG.rst b/CHANGELOG.rst\n--- a/CHANGELOG.rst\n+++ b/CHANGELOG.rst\n@@ -1,1 +1,2 @@\n x\n+fixed\n" +
		"diff --git a/src/m.py b/src/m.py\n--- a/src/m.py\n+++ b/src/m.py\n@@ -1,1 +1,1 @@\n-a\n+b\n@@ -9,1 +9,1 @@\n-c\n+d\n"
	out := hunkAblations(patch, 10)
	if len(out) != 2 {
		t.Fatalf("%d ablations, want 2 (one per src/m.py hunk)", len(out))
	}
	for _, p := range out {
		if !strings.Contains(p, "CHANGELOG.rst") {
			t.Fatalf("an ablation drops the changelog hunk:\n%s", p)
		}
	}
	for path, want := range map[string]bool{"CHANGELOG.rst": true, "docs/api.md": true, "README": true, "requirements-dev.txt": false, "src/m.py": false} {
		if docPath(path) != want {
			t.Errorf("docPath(%q) = %v", path, !want)
		}
	}
}
