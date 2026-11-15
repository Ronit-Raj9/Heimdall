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
// It holds a relative symlink that stays inside the repository, which every
// step (task init, validate, admission) must carry as a link.
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
	write("docs/notes.md", "Signs matter.\n")
	if err := os.Symlink("../docs/notes.md", filepath.Join(dir, "calc", "NOTES.md")); err != nil {
		t.Fatal(err)
	}
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

// TestRunTaskAdmitRefusesInvalidTask checks that `task admit` and `suite
// admit` run the static validation first and refuse, before any docker
// command and without writing suite.lock, a draft task and a task with a
// symlink out of its folder. The docker on PATH here only records that it
// was called.
func TestRunTaskAdmitRefusesInvalidTask(t *testing.T) {
	src, base, ref := pythonSource(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	bin := t.TempDir()
	called := filepath.Join(bin, "docker-called")
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho \"$@\" >> "+called+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"init", "py", "-template", "python-pytest", "-config", config}); code != exitOK {
		t.Fatalf("suite init exit %d", code)
	}
	if code := runTask([]string{"init", "-config", config, "-suite", "py", "-id", "t01", "-repo", src, "-base", base, "-reference", ref}); code != exitOK {
		t.Fatalf("task init exit %d", code)
	}
	task := filepath.Join(dir, "benchmarks", "py", "tasks", "t01")
	lock := filepath.Join(dir, "benchmarks", "py", "suite.lock")
	admit := func(name string) {
		t.Helper()
		if code := runTask([]string{"admit", "-config", config, "py/t01"}); code == exitOK {
			t.Errorf("%s: task admit exit 0", name)
		}
		if code := runSuite([]string{"admit", "-config", config, "py"}); code == exitOK {
			t.Errorf("%s: suite admit exit 0", name)
		}
		if b, err := os.ReadFile(called); err == nil {
			t.Errorf("%s: docker ran before static validation refused: %s", name, b)
		}
		if _, err := os.Stat(lock); !os.IsNotExist(err) {
			t.Errorf("%s: a refused admission wrote suite.lock", name)
		}
	}
	admit("draft instruction")
	if err := os.WriteFile(filepath.Join(task, "instruction.md"), []byte("# Keep signs\n\n`add(-2, 3)` returns 5; it should return 1.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(outside, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(task, "repo", "calc", "key.txt")); err != nil {
		t.Fatal(err)
	}
	admit("symlink out of the task")
}

func TestRunScenarioTaskInit(t *testing.T) {
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	for _, args := range [][]string{
		{"init", "desk", "-template", "mcp", "-config", config},
		{"init", "py", "-template", "python-pytest", "-config", config},
	} {
		if code := runSuite(args); code != exitOK {
			t.Fatalf("suite %v: exit %d", args, code)
		}
	}
	if code := runTask([]string{"validate", "-config", config, "desk/restock"}); code != exitOK {
		t.Fatalf("validate the sample: exit %d", code)
	}
	if code := runTask([]string{"init", "-config", config, "-suite", "desk", "-id", "t02"}); code != exitOK {
		t.Fatalf("task init t02: exit %d", code)
	}
	if code := runTask([]string{"validate", "-config", config, "desk/t02"}); code != 1 {
		t.Fatalf("validate a copied task with EDIT ME markers: exit %d, want 1", code)
	}
	if code := runTask([]string{"init", "-config", config, "-suite", "desk", "-id", "t03", "-kind", "mcp", "-from", "t02", "-license", "MIT"}); code != exitOK {
		t.Fatalf("task init -kind mcp -from t02: exit %d", code)
	}
	meta, err := os.ReadFile(filepath.Join(dir, "benchmarks", "desk", "tasks", "t03", "task.yaml"))
	if err != nil || !strings.Contains(string(meta), "license: MIT") || !strings.Contains(string(meta), "from task t02") {
		t.Fatalf("t03 task.yaml: %v\n%s", err, meta)
	}
	suiteYAML, err := os.ReadFile(filepath.Join(dir, "benchmarks", "desk", "suite.yaml"))
	if err != nil || !strings.Contains(string(suiteYAML), "tasks: [restock, t02, t03]") {
		t.Fatalf("suite.yaml tasks: %v\n%s", err, suiteYAML)
	}
	for _, bad := range [][]string{
		{"init", "-config", config, "-suite", "desk", "-id", "t04", "-kind", "qa"},
		{"init", "-config", config, "-suite", "py", "-id", "t01"},
		{"init", "-config", config, "-suite", "desk", "-id", "t05", "-kind", "mcp", "-repo", dir},
		{"init", "-config", config, "-suite", "desk", "-id", "t02"},
	} {
		if code := runTask(bad); code == exitOK {
			t.Errorf("task %v: exit 0", bad)
		}
	}
}
