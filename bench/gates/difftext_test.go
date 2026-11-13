package gates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleDiff = `diff --git a/setup.cfg b/setup.cfg
index 1111111..2222222 100644
--- a/setup.cfg
+++ b/setup.cfg
@@ -1,4 +1,5 @@
 [metadata]
 name = demo
--- a removed line that looks like a header
+++ an added line that looks like a header
+
 [tool:pytest]
diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+hello
\ No newline at end of file
diff --git a/gone.py b/gone.py
deleted file mode 100644
--- a/gone.py
+++ /dev/null
@@ -1,2 +0,0 @@
-x = 1
-y = 2
diff --git "a/sp ace.py" "b/sp ace.py"
--- "a/sp ace.py"
+++ "b/sp ace.py"
@@ -1 +1 @@
-a
+b
`

func TestParseUnifiedDiff(t *testing.T) {
	files, err := parseUnifiedDiff([]byte(sampleDiff))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key        string
		oldPath    string
		hunks      int
		firstLines int
	}{
		{"setup.cfg", "setup.cfg", 1, 6},
		{"new.txt", "", 1, 1},
		{"gone.py", "gone.py", 1, 2},
		{"sp ace.py", "sp ace.py", 1, 2},
	}
	if len(files) != len(cases) {
		t.Fatalf("got %d files: %v", len(files), keys(files))
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			fd := files[c.key]
			if fd == nil {
				t.Fatalf("missing %q in %v", c.key, keys(files))
			}
			if fd.OldPath != c.oldPath || len(fd.Hunks) != c.hunks || len(fd.Hunks[0].Lines) != c.firstLines {
				t.Fatalf("got old=%q hunks=%d lines=%v", fd.OldPath, len(fd.Hunks), fd.Hunks[0].Lines)
			}
		})
	}
	if got := files["setup.cfg"].Hunks[0].Lines[2]; got != "--- a removed line that looks like a header" {
		t.Fatalf("hunk body misparsed: %q", got)
	}
}

func TestParseUnifiedDiffErrors(t *testing.T) {
	cases := map[string]string{
		"hunk before header": "@@ -1 +1 @@\n-a\n+b\n",
		"bad hunk header":    "--- a/x\n+++ b/x\n@@ -x +1 @@\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseUnifiedDiff([]byte(text)); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestReadDiffText(t *testing.T) {
	p := filepath.Join(t.TempDir(), "workspace.diff")
	if err := os.WriteFile(p, []byte(sampleDiff), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := readDiffText(p)
	if err != nil || !strings.Contains(string(b), "setup.cfg") {
		t.Fatalf("read: %v", err)
	}
	if _, err := readDiffText(filepath.Join(t.TempDir(), "missing.diff")); err == nil {
		t.Fatal("want error for a missing file")
	}
}

func keys(m map[string]*fileDiff) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
