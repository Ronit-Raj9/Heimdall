package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/quickstart"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
)

// First workflow tests: init inside a repository, compare's experiment
// file per flag form, ci install, task review and the program name.

// quickRepo is a small Python repository with an AGENTS.md that changes in
// the last commit. It returns the folder and the two commits.
func quickRepo(t *testing.T) (string, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := filepath.Join(t.TempDir(), "lib")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pyproject.toml", "[tool.pytest.ini_options]\ntestpaths = ['tests']\n")
	write("calc/__init__.py", "")
	write("calc/core.py", "def add(a, b):\n    return abs(a) + abs(b)\n")
	write("tests/test_core.py", "from calc.core import add\n\n\ndef test_pos():\n    assert add(2, 3) == 5\n")
	write("AGENTS.md", "Run the tests.\n")
	gitFixture(t, dir, "init", "-q", "-b", "main")
	gitFixture(t, dir, "-c", "user.name=T", "-c", "user.email=t@example.com", "add", "-A")
	gitFixture(t, dir, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "Initial")
	base := gitFixture(t, dir, "rev-parse", "HEAD")
	write("calc/core.py", "def add(a, b):\n    return a + b\n")
	write("tests/test_core.py", "from calc.core import add\n\n\ndef test_pos():\n    assert add(2, 3) == 5\n\n\ndef test_sign():\n    assert add(-2, 3) == 1\n")
	write("AGENTS.md", "Run the tests. Never edit tests.\n")
	gitFixture(t, dir, "-c", "user.name=T", "-c", "user.email=t@example.com", "add", "-A")
	gitFixture(t, dir, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "add keeps the sign")
	ref := gitFixture(t, dir, "rev-parse", "HEAD")
	return dir, base, ref
}

// fakeMachine is a probe with no agent CLI, Docker up and every port free.
func fakeMachine() quickstart.Probe {
	return quickstart.Probe{
		LookPath: func(name string) (string, error) {
			if name == "docker" || name == "claude" {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Output: func(_ context.Context, name string, args ...string) (string, error) {
			if strings.HasSuffix(name, "claude") {
				return "2.1.0 (Claude Code)", nil
			}
			return "27.0.0", nil
		},
		PortFree: func(int) bool { return true },
	}
}

func initQuickRepo(t *testing.T) (dir, base, ref string) {
	t.Helper()
	dir, base, ref = quickRepo(t)
	old := detectProbe
	detectProbe = fakeMachine
	t.Cleanup(func() { detectProbe = old })
	t.Chdir(dir)
	if code := runInit(nil); code != exitOK {
		t.Fatalf("init in a repository: exit %d", code)
	}
	return dir, base, ref
}

func TestInitInRepository(t *testing.T) {
	dir, _, _ := initQuickRepo(t)
	pf, err := quickstart.LoadProjectFile(filepath.Join(dir, ".heimdall"))
	if err != nil {
		t.Fatal(err)
	}
	if pf.Suite != "lib" || pf.Tests.Command != "python -m pytest" || len(pf.Agents) != 1 || pf.Agents[0].Config != "agents/claude-code.yaml" {
		t.Errorf("heimdall.yaml = %+v", pf)
	}
	for _, rel := range []string{"deploy/bench.yaml", "deploy/.env", "agents/claude-code.yaml", "benchmarks/lib/suite.yaml", "policies/default.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, ".heimdall", rel)); err != nil {
			t.Errorf("missing .heimdall/%s", rel)
		}
	}
	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !strings.Contains(string(gi), quickstart.RunsIgnore) {
		t.Errorf(".gitignore = %q, %v", gi, err)
	}
	if st := gitFixture(t, dir, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Errorf("init changed tracked files: %s", st)
	}
	// A second init keeps everything; -detect with a test command override.
	if code := runInit([]string{"-detect", "-test-cmd", "make check"}); code != exitOK {
		t.Fatalf("second init exit %d", code)
	}
	if got := projectConfig(defaultConfig, false); got != filepath.Join(dir, ".heimdall", "deploy", "bench.yaml") {
		t.Errorf("projectConfig = %s", got)
	}
	if code := runInit([]string{"-detect", "a", "b"}); code != exitUsage {
		t.Errorf("two folders: exit %d", code)
	}
}

func TestCompareExperimentForms(t *testing.T) {
	dir, base, ref := initQuickRepo(t)
	cfgPath := filepath.Join(dir, ".heimdall", "deploy", "bench.yaml")
	cfg, err := lib.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f := compareFlags{a: "agents/oracle.yaml", b: "agents/noop.yaml", trials: 1, concurrency: 2, repo: dir, paths: strings.Join(scaffold.DefaultAgentPaths, ","), name: "configs"}
	s, err := buildCompare(ctx, f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := loadExperiment(s.path)
	if err != nil || exp.Suite != "lib" || exp.Base != "agents/oracle.yaml" || exp.Arms["B"].Agent != "agents/noop.yaml" || !exp.Smoke || !s.opts.Smoke {
		t.Fatalf("configs form: %+v %v", exp, err)
	}
	if _, err := buildCompare(ctx, f, cfg); err == nil {
		t.Error("a second compare with the same --name overwrote the file")
	}

	f = compareFlags{a: "model=claude-sonnet-4-5", b: "model=claude-haiku-4-5", trials: 3, faults: listFlag{"model-outage"}, budget: 2, repo: dir, name: "models"}
	s, err = buildCompare(ctx, f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	exp, err = loadExperiment(s.path)
	if err != nil || exp.Base != "agents/claude-code.yaml" || exp.Arms["B"].Set["model"] != "claude-haiku-4-5" || len(exp.Conditions) != 2 || exp.Smoke {
		t.Fatalf("models form: %+v %v", exp, err)
	}

	f = compareFlags{a: "model=claude-sonnet-4-5", b: "model=claude-haiku-4-5", trials: 1, repo: dir, name: "zero-budget"}
	if _, err := buildCompare(ctx, f, cfg); err == nil || !strings.Contains(err.Error(), "--budget-usd") {
		t.Errorf("a real-model compare with --budget-usd 0 was written uncapped: %v", err) // audit 5-B-02
	}
	if _, err := os.Stat(filepath.Join(dir, ".heimdall", "experiments", "zero-budget.yaml")); err == nil {
		t.Error("the refused zero-budget experiment file was written")
	}

	f = compareFlags{baseline: base, candidate: ref, trials: 3, budget: 2, repo: dir, paths: strings.Join(scaffold.DefaultAgentPaths, ","), name: "refs"}
	s, err = buildCompare(ctx, f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	exp, err = loadExperiment(s.path)
	if err != nil {
		t.Fatal(err)
	}
	notesA, _ := exp.Arms["A"].Set["notes.user_file"].(string)
	notesB, _ := exp.Arms["B"].Set["notes.user_file"].(string)
	if notesA != "experiments/fixtures/refs/A/AGENTS.md" || notesB != "experiments/fixtures/refs/B/AGENTS.md" {
		t.Fatalf("refs form notes: %q %q", notesA, notesB)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".heimdall", filepath.FromSlash(notesB))); !strings.Contains(string(b), "Never edit tests") {
		t.Errorf("candidate notes = %q", b)
	}
	f.name, f.candidate = "same", base
	if _, err := buildCompare(ctx, f, cfg); err == nil || !strings.Contains(err.Error(), "same agent files") {
		t.Errorf("identical refs: %v", err)
	}
	if code := runCompare([]string{"--a", "agents/oracle.yaml"}); code != exitUsage {
		t.Errorf("one arm: exit %d", code)
	}
	if code := runCompare([]string{"--a", "x.yaml", "--b", "y.yaml", "--baseline", "main", "--candidate", "HEAD"}); code != exitUsage {
		t.Errorf("two forms at once: exit %d", code)
	}
	if code := runCompare([]string{"--a", "agents/oracle.yaml", "--b", "agents/noop.yaml", "--name", "wo", "--write-only"}); code != exitOK {
		t.Errorf("--write-only exit %d", code)
	}
}

func TestCIInstall(t *testing.T) {
	dir, _, _ := initQuickRepo(t)
	wf := filepath.Join(dir, ".github", "workflows", "heimdall.yml")
	if code := runCI([]string{"install", "github", "-dry-run"}); code != exitOK {
		t.Fatalf("dry-run exit %d", code)
	}
	if _, err := os.Stat(wf); err == nil {
		t.Fatal("-dry-run wrote the workflow")
	}
	if code := runCI([]string{"install", "github", "-mode", "live"}); code != exitOK {
		t.Fatalf("live exit %d", code)
	}
	b, err := os.ReadFile(wf)
	if err != nil || !strings.Contains(string(b), "--base agents/claude-code.yaml") || !strings.Contains(string(b), "self-hosted") {
		t.Fatalf("workflow: %v\n%s", err, b)
	}
	if code := runCI([]string{"install", "github"}); code == exitOK {
		t.Error("existing workflow replaced without -force")
	}
	if code := runCI([]string{"install", "gitlab"}); code != exitUsage {
		t.Errorf("gitlab: exit %d", code)
	}
}

func TestTaskReviewCommand(t *testing.T) {
	dir, base, ref := initQuickRepo(t)
	suiteDir := filepath.Join(dir, ".heimdall", "benchmarks", "lib")
	res, err := scaffold.InitTask(context.Background(), scaffold.TaskInitOptions{SuiteDir: suiteDir, ID: "m-1", Repo: dir, Base: base, Reference: ref,
		ExcludeDir: filepath.Join(dir, ".heimdall")})
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.MarkMined(res.Dir, []byte("# Keep the sign\n"), "python -m pytest"); err != nil {
		t.Fatal(err)
	}
	s, err := loadSuite(context.Background(), filepath.Join(dir, ".heimdall", "benchmarks"), "lib")
	if err != nil {
		t.Fatal(err)
	}
	if line := unreviewedLine(s, nil); !strings.HasPrefix(line, "1 of 1 tasks have unreviewed instructions") {
		t.Errorf("line = %q", line)
	}
	if code := runTask([]string{"review", "-no-admit", "m-1"}); code != exitOK {
		t.Fatalf("review exit %d", code)
	}
	s, _ = loadSuite(context.Background(), filepath.Join(dir, ".heimdall", "benchmarks"), "lib")
	if n, m := unreviewedCount(s, nil); n != 0 || m != 1 {
		t.Errorf("after review: %d of %d", n, m)
	}
	if code := runTask([]string{"review", "-no-admit", "lib/nope"}); code == exitOK {
		t.Error("review of a missing task succeeded")
	}
}

func TestProgramName(t *testing.T) {
	for argv0, want := range map[string]string{"/usr/bin/heimdall": "heimdall", "heimdall.exe": "heimdall", "./bin/bifrost-bench": "bifrost-bench", "/tmp/go-build1/exe/main": "bifrost-bench"} {
		if got := programName(argv0); got != want {
			t.Errorf("programName(%q) = %q, want %q", argv0, got, want)
		}
	}
	in := "usage: bifrost-bench task init -suite s\n       bifrost-bench task admit s/t\nimage bench/bifrost-bench:dev\nrun bifrost-bench later\n"
	want := "usage: heimdall task init -suite s\n       heimdall task admit s/t\nimage bench/bifrost-bench:dev\nrun bifrost-bench later\n"
	if got := renameUsage(in, "heimdall"); got != want {
		t.Errorf("renameUsage:\n%s", got)
	}
	if got := renameUsage(in, "bifrost-bench"); got != in {
		t.Error("renameUsage changed text under the bifrost-bench name")
	}
}

func TestMineLockCarryRules(t *testing.T) {
	a := []byte("# template: python-pytest\nid: s\nversion: 0.1.0\ntasks: [m-1]\nprobes: [model-outage]\n")
	b := []byte("# other comment\nid: s\nversion: 0.1.0\ntasks:\n  - m-1\n  - m-2\nprobes: [model-outage]\n")
	c := []byte("id: s\nversion: 0.2.0\ntasks: [m-1]\nprobes: [model-outage]\n")
	if ok, err := sameExceptTasks(a, b); err != nil || !ok {
		t.Errorf("tasks-only change: %v %v", ok, err)
	}
	if ok, _ := sameExceptTasks(a, c); ok {
		t.Error("a version change counted as tasks-only")
	}
	fp := schemas.Fingerprint{Settings: "s", Files: "f", Images: map[string]string{"task": "sha256:1"}}
	if !sameFingerprint(fp, schemas.Fingerprint{Settings: "s", Files: "f", Images: map[string]string{"task": "sha256:1"}}) {
		t.Error("equal fingerprints differ")
	}
	for _, other := range []schemas.Fingerprint{{Settings: "x", Files: "f"}, {Settings: "s", Files: "x", Images: fp.Images}, {Settings: "s", Files: "f", Images: map[string]string{"task": "sha256:2"}}} {
		if sameFingerprint(fp, other) {
			t.Errorf("%+v counted as equal", other)
		}
	}
}
