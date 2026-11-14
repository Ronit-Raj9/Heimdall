package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSplitTaskRef(t *testing.T) {
	tests := []struct {
		ref        string
		suite, tsk string
		ok         bool
	}{
		{"web/t01", "web", "t01", true},
		{"/web/t01/", "web", "t01", true},
		{"web", "", "", false},
		{"web/", "", "", false},
		{"a/b/c", "", "", false},
	}
	for _, tc := range tests {
		s, tk, ok := splitTaskRef(tc.ref)
		if s != tc.suite || tk != tc.tsk || ok != tc.ok {
			t.Errorf("splitTaskRef(%q) = %q %q %v", tc.ref, s, tk, ok)
		}
	}
	if got := splitList(" a, ,b,"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("splitList = %v", got)
	}
}

// gitFixture runs git in dir with the user's configuration shut out.
func gitFixture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// pythonSource builds a repository with a bug and its fix plus a new test.
func pythonSource(t *testing.T) (dir, base, ref string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir = filepath.Join(t.TempDir(), "app")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("calc/__init__.py", "")
	write("calc/core.py", "def add(a, b):\n    return abs(a) + abs(b)\n")
	write("tests/test_core.py", "from calc.core import add\n\n\ndef test_pos():\n    assert add(2, 3) == 5\n")
	gitFixture(t, dir, "init", "-q", "-b", "main")
	gitFixture(t, dir, "add", "-A")
	gitFixture(t, dir, "commit", "-q", "-m", "Initial")
	base = gitFixture(t, dir, "rev-parse", "HEAD")
	write("calc/core.py", "def add(a, b):\n    return a + b\n")
	write("tests/test_core.py", "from calc.core import add\n\n\ndef test_pos():\n    assert add(2, 3) == 5\n\n\ndef test_neg():\n    assert add(-2, 3) == 1\n")
	gitFixture(t, dir, "add", "-A")
	gitFixture(t, dir, "commit", "-q", "-m", "Keep signs in add")
	ref = gitFixture(t, dir, "rev-parse", "HEAD")
	return dir, base, ref
}

func TestRunTaskInitAndValidate(t *testing.T) {
	src, base, ref := pythonSource(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"init", "py", "-template", "python-pytest", "-config", config}); code != exitOK {
		t.Fatalf("suite init exit %d", code)
	}
	args := []string{"init", "-config", config, "-suite", "py", "-id", "t01", "-repo", src, "-base", base, "-reference", ref, "-capabilities", "test_discipline,planning", "-difficulty", "medium"}
	if code := runTask(args); code != exitOK {
		t.Fatalf("task init exit %d", code)
	}
	if code := runTask(args); code != exitInfra {
		t.Fatalf("task init over an existing task: exit %d", code)
	}
	task := filepath.Join(dir, "benchmarks", "py", "tasks", "t01")
	yaml, err := os.ReadFile(filepath.Join(task, "task.yaml"))
	if err != nil || !strings.Contains(string(yaml), `capabilities: ["test_discipline", "planning"]`) || !strings.Contains(string(yaml), "difficulty: medium") {
		t.Fatalf("task.yaml: %v\n%s", err, yaml)
	}
	if code := runTask([]string{"validate", "-config", config, "py/t01"}); code != 1 {
		t.Fatalf("validate a draft: exit %d, want 1 (EDIT ME)", code)
	}
	if err := os.WriteFile(filepath.Join(task, "instruction.md"), []byte("# Keep signs\n\n`add(-2, 3)` returns 5; it should return 1.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runTask([]string{"validate", "py/t01", "-config", config}); code != exitOK {
		t.Fatalf("validate an edited task: exit %d", code)
	}
	for _, bad := range [][]string{{"validate", "py"}, {"init", "-suite", "py"}, {"admit"}, {"frobnicate"}} {
		if code := runTask(bad); code == exitOK {
			t.Errorf("task %v: exit 0", bad)
		}
	}
}

func TestValidateSummary(t *testing.T) {
	tests := []struct {
		problems, errs int
		want           string
	}{
		{0, 0, "no problems"},
		{4, 1, "1 error, 3 warnings"},
		{3, 0, "0 errors, 3 warnings"},
		{2, 2, "2 errors, 0 warnings"},
		{2, 1, "1 error, 1 warning"},
	}
	for _, tt := range tests {
		if got := validateSummary(tt.problems, tt.errs); got != tt.want {
			t.Errorf("validateSummary(%d, %d) = %q, want %q", tt.problems, tt.errs, got, tt.want)
		}
	}
}
