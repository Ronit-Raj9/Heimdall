package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/suite"
)

// newSuiteDir writes a suite from a template under a fresh benchmarks folder
// and returns that folder and the suite folder.
func newSuiteDir(t *testing.T, id, tmpl string) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "benchmarks")
	files, err := Suite(SuiteOptions{ID: id, Template: tmpl})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, id)
	if err := WriteFiles(dir, files, false); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// pythonRepo is a two-commit repository: a sign bug, then its fix with one
// changed test file, a new test and a script whose mode must survive.
func pythonRepo(t *testing.T) (*testRepo, string, string) {
	r := newTestRepo(t)
	r.write("calc/__init__.py", "")
	r.write("calc/core.py", "def add(a, b):\n    return abs(a) + abs(b)\n")
	r.write("conftest.py", "import pytest\n\n\n@pytest.fixture\ndef zero():\n    return 0\n")
	r.write("tests/conftest.py", "import pytest\n\n\n@pytest.fixture\ndef pair():\n    return (2, 3)\n")
	r.write("tests/test_core.py", "from calc.core import add\n\n\ndef test_pos(pair):\n    assert add(*pair) == 5\n")
	r.write("scripts/run.sh", "#!/bin/sh\necho run\n")
	if err := os.Chmod(filepath.Join(r.dir, "scripts/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := r.commit("Initial")
	r.write("calc/core.py", "def add(a, b):\n    return a + b\n")
	r.write("tests/test_core.py", "from calc.core import add\n\n\ndef test_pos(pair):\n    assert add(*pair) == 5\n\n\ndef test_neg():\n    assert add(-2, 3) == 1\n")
	r.write("tests/test_more.py", "from calc.core import add\n\n\nclass TestMore:\n    def test_both(self):\n        assert add(-1, -1) == -2\n")
	ref := r.commit("Keep signs in add\n\nadd() dropped the sign of negative numbers.\n\nSigned-off-by: Test <test@example.com>")
	return r, base, ref
}

func TestInitTaskPython(t *testing.T) {
	r, base, ref := pythonRepo(t)
	root, suiteDir := newSuiteDir(t, "py", TemplatePythonPytest)
	res, err := InitTask(context.Background(), TaskInitOptions{
		SuiteDir: suiteDir, ID: "t01", Repo: r.dir, Base: base, Reference: ref,
		Capabilities: []string{"test_discipline"}, Difficulty: "easy", License: "MIT",
	})
	if err != nil {
		t.Fatal(err)
	}
	task := res.Dir
	if res.Template != TemplatePythonPytest || !res.SuiteEdited {
		t.Fatalf("result = %+v", res)
	}
	if want := []string{"tests/test_core.py", "tests/test_more.py"}; !reflect.DeepEqual(res.Hidden, want) {
		t.Fatalf("hidden = %v", res.Hidden)
	}
	if want := []string{"tests/conftest.py", "tests/test_core.py"}; !reflect.DeepEqual(res.Protected, want) {
		t.Fatalf("protected = %v", res.Protected)
	}
	wantReq := []string{"tests/test_core.py::test_pos", "tests/test_core.py::test_neg", "tests/test_more.py::TestMore::test_both"}
	if !reflect.DeepEqual(res.Required, wantReq) {
		t.Fatalf("required = %v", res.Required)
	}
	checks := map[string]string{
		"repo/calc/core.py":                 "abs(a)",
		"repo/tests/test_core.py":           "def test_pos",
		"verifier/tests/tests/test_core.py": "def test_neg",
		"verifier/tests/tests/test_more.py": "class TestMore",
		"verifier/tests/tests/conftest.py":  "def pair",
		"verifier/tests/repo_conftest.py":   "def zero",
		"verifier/tests/conftest.py":        "sys.path.insert(0, _path)",
		"solution/reference.diff":           "+    return a + b",
		"controls/exploit.sh":               "pytest_runtest_makereport",
		"instruction.md":                    "# Keep signs in add\n\nadd() dropped the sign of negative numbers.",
		"task.yaml":                         "reference_commit: " + ref,
		"repo/scripts/run.sh":               "echo run",
		"../../suite.yaml":                  "tasks: [t01]",
	}
	for rel, want := range checks {
		if got := readFile(t, filepath.Join(task, filepath.FromSlash(rel))); !strings.Contains(got, want) {
			t.Errorf("%s does not contain %q:\n%s", rel, want, got)
		}
	}
	if strings.Contains(readFile(t, filepath.Join(task, "repo/tests/test_core.py")), "test_neg") {
		t.Error("the hidden test leaked into repo/")
	}
	if _, err := os.Stat(filepath.Join(task, "repo/tests/test_more.py")); !os.IsNotExist(err) {
		t.Error("a test file the reference adds is in repo/")
	}
	if strings.Contains(readFile(t, filepath.Join(task, "solution/reference.diff")), "tests/") {
		t.Error("the reference diff carries test files")
	}
	if strings.Contains(readFile(t, filepath.Join(task, "instruction.md")), "Signed-off-by") {
		t.Error("instruction keeps the sign-off trailer")
	}
	for rel, mode := range map[string]os.FileMode{"repo/scripts/run.sh": 0o755, "controls/exploit.sh": 0o755, "repo/calc/core.py": 0o644, ".": 0o755} {
		if fi, err := os.Stat(filepath.Join(task, rel)); err != nil || fi.Mode().Perm() != mode {
			t.Errorf("%s mode = %v (%v), want %o", rel, fi.Mode().Perm(), err, mode)
		}
	}
	if fi, err := os.Stat(filepath.Join(task, "controls/empty.diff")); err != nil || fi.Size() != 0 {
		t.Errorf("empty.diff: %v %v", fi, err)
	}
	if err := suite.ApplyCheck(context.Background(), filepath.Join(task, "repo"), filepath.Join(task, "solution/reference.diff")); err != nil {
		t.Errorf("reference diff does not apply: %v", err)
	}
	if status := r.git("status", "--porcelain"); status != "" || r.git("rev-parse", "HEAD") != ref {
		t.Errorf("source repository changed: %q", status)
	}
	assertTaskLoads(t, root, "py", "t01", 1)
}

// assertTaskLoads loads the task through the suite package and expects
// exactly wantErrs lint errors (the EDIT ME marker of the draft).
func assertTaskLoads(t *testing.T, root, suiteID, taskID string, wantErrs int) {
	t.Helper()
	s, task, err := suite.LoadTask(root, suiteID, taskID)
	if err != nil {
		t.Fatalf("load %s/%s: %v", suiteID, taskID, err)
	}
	if task.Provenance == nil || task.Provenance.CreatedBy != "task init" {
		t.Errorf("provenance = %+v", task.Provenance)
	}
	var errs []string
	for _, p := range suite.ValidateTask(context.Background(), s, task) {
		if p.Severity == suite.SeverityError {
			errs = append(errs, p.Path+": "+p.Message)
		}
	}
	if len(errs) != wantErrs || (wantErrs > 0 && !strings.Contains(strings.Join(errs, "\n"), "EDIT ME")) {
		t.Errorf("validate errors = %v, want %d (the EDIT ME marker)", errs, wantErrs)
	}
}

func TestInitTaskNode(t *testing.T) {
	r := newTestRepo(t)
	r.write("package.json", `{"name":"m","type":"module"}`+"\n")
	r.write("src/math.js", "export function clamp(x, lo, hi) {\n  return Math.max(lo, x);\n}\n")
	r.write("test/math.test.js", "import { test } from 'node:test';\nimport { clamp } from '../src/math.js';\ntest('below', () => {});\n")
	base := r.commit("Add clamp")
	r.write("src/math.js", "export function clamp(x, lo, hi) {\n  return Math.min(hi, Math.max(lo, x));\n}\n")
	r.write("test/math.test.js", "import { test } from 'node:test';\nimport { clamp } from '../src/math.js';\ntest('below', () => {});\ntest('above', () => {});\n")
	r.write("src/inside.test.ts", "import { test } from 'node:test';\ntest('inside', () => {});\n")
	r.write("test/fixtures/data.json", "{}\n")
	ref := r.commit("Clamp the upper bound")
	root, suiteDir := newSuiteDir(t, "js", TemplateNodeTest)
	res, err := InitTask(context.Background(), TaskInitOptions{SuiteDir: suiteDir, ID: "n01", Repo: r.dir, Base: base, Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"src/inside.test.ts", "test/fixtures/data.json", "test/math.test.js"}; !reflect.DeepEqual(res.Hidden, want) {
		t.Fatalf("hidden = %v", res.Hidden)
	}
	if !reflect.DeepEqual(res.Protected, []string{"test/math.test.js"}) || len(res.Required) != 0 {
		t.Fatalf("protected %v required %v", res.Protected, res.Required)
	}
	for _, rel := range []string{"verifier/tests/test/math.test.js", "verifier/tests/src/inside.test.ts", "verifier/tests/test/fixtures/data.json", "repo/test/math.test.js", "controls/exploit.sh"} {
		if _, err := os.Stat(filepath.Join(res.Dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "verifier/tests/conftest.py")); !os.IsNotExist(err) {
		t.Error("node task got the python bootstrap")
	}
	assertTaskLoads(t, root, "js", "n01", 1)
}

func TestInitTaskGo(t *testing.T) {
	r := newTestRepo(t)
	r.write("go.mod", "module example.com/calc\n\ngo 1.22\n")
	r.write("calc.go", "package calc\n\nfunc Add(a, b int) int { return a - b }\n")
	r.write("calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAddZero(t *testing.T) {}\n")
	r.write("tools/gen/go.mod", "module example.com/gen\n\ngo 1.22\n")
	r.write("tools/gen/gen.go", "package gen\n")
	base := r.commit("Calc")
	r.write("calc.go", "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
	r.write("calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAddZero(t *testing.T) {}\nfunc TestAddTwo(t *testing.T) {}\n")
	r.write("internal/ops/ops.go", "package ops\n")
	r.write("internal/ops/ops_test.go", "package ops\n\nimport \"testing\"\n\nfunc TestOps(t *testing.T) {}\n")
	r.write("tools/gen/gen_test.go", "package gen\n\nimport \"testing\"\n\nfunc TestGen(t *testing.T) {}\n")
	ref := r.commit("Fix Add")
	root, suiteDir := newSuiteDir(t, "gosuite", TemplateGoTest)
	res, err := InitTask(context.Background(), TaskInitOptions{SuiteDir: suiteDir, ID: "g01", Repo: r.dir, Base: base, Reference: ref, Kind: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example.com/calc::TestAddZero", "example.com/calc::TestAddTwo", "example.com/calc/internal/ops::TestOps", "example.com/gen::TestGen"}
	if !reflect.DeepEqual(res.Required, want) {
		t.Fatalf("required = %v, want %v", res.Required, want)
	}
	if !strings.Contains(readFile(t, filepath.Join(res.Dir, "task.yaml")), "kind: feature") {
		t.Error("kind not recorded")
	}
	assertTaskLoads(t, root, "gosuite", "g01", 1)
}

func TestInitTaskSubfolder(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "mono\n")
	r.write("services/api/app.py", "X = 1\n")
	r.write("services/api/tests/test_app.py", "def test_x():\n    pass\n")
	base := r.commit("api")
	r.write("services/api/app.py", "X = 2\n")
	r.write("services/api/tests/test_app.py", "def test_x():\n    pass\n\n\ndef test_y():\n    pass\n")
	inside := r.commit("api fix")
	r.write("README.md", "mono 2\n")
	r.write("services/api/app.py", "X = 3\n")
	r.write("services/api/tests/test_app.py", "def test_z():\n    pass\n")
	outside := r.commit("touches the root")

	_, suiteDir := newSuiteDir(t, "py", TemplatePythonPytest)
	sub := filepath.Join(r.dir, "services", "api")
	res, err := InitTask(context.Background(), TaskInitOptions{SuiteDir: suiteDir, ID: "s01", Repo: sub, Base: base, Reference: inside})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "repo", "app.py")); err != nil {
		t.Errorf("repo/app.py: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "repo", "README.md")); !os.IsNotExist(err) {
		t.Error("a file outside the folder is in repo/")
	}
	if !strings.Contains(readFile(t, filepath.Join(res.Dir, "task.yaml")), `//services/api"`) || !strings.Contains(readFile(t, filepath.Join(res.Dir, "task.yaml")), `project: "api"`) {
		t.Error("source does not name the folder")
	}
	if err := suite.ApplyCheck(context.Background(), filepath.Join(res.Dir, "repo"), filepath.Join(res.Dir, "solution/reference.diff")); err != nil {
		t.Errorf("reference diff does not apply inside the folder: %v", err)
	}
	_, err = InitTask(context.Background(), TaskInitOptions{SuiteDir: suiteDir, ID: "s02", Repo: sub, Base: inside, Reference: outside})
	if err == nil || !strings.Contains(err.Error(), "outside services/api/") || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("want an outside-the-folder error, got %v", err)
	}
}

func TestInitTaskRefusals(t *testing.T) {
	r := newTestRepo(t)
	r.write("app.py", "X = 1\n")
	r.write("tests/test_app.py", "def test_x():\n    pass\n")
	base := r.commit("base")
	r.write("app.py", "X = 2\n")
	noTests := r.commit("code only")
	r.write("tests/test_app.py", "def test_x():\n    pass\n\n\ndef test_y():\n    pass\n")
	onlyTests := r.commit("tests only")
	r.write("app.py", "X = 3\n")
	r.write("tests/test_app.py", "def test_z():\n    pass\n")
	if err := os.Symlink("../../../etc/passwd", filepath.Join(r.dir, "escape")); err != nil {
		t.Fatal(err)
	}
	r.commit("bad link")
	r.remove("escape")
	r.write("app.py", "X = 4\n")
	r.write("tests/test_app.py", "def test_w():\n    pass\n")
	linkBase := r.git("rev-parse", "HEAD")
	good := r.commit("good")

	_, suiteDir := newSuiteDir(t, "py", TemplatePythonPytest)
	tests := []struct {
		name      string
		opts      TaskInitOptions
		wantError string
	}{
		{"not an ancestor", TaskInitOptions{Base: onlyTests, Reference: base}, "is not an ancestor"},
		{"same commit", TaskInitOptions{Base: base, Reference: base}, "same commit"},
		{"unknown commit", TaskInitOptions{Base: "nope", Reference: base}, `commit "nope" not found`},
		{"no test files changed", TaskInitOptions{Base: base, Reference: noTests}, "no test files matching tests/**,test_*.py,*_test.py; pass -tests"},
		{"only test files changed", TaskInitOptions{Base: noTests, Reference: onlyTests}, "only test files"},
		{"symlink leaves the repo", TaskInitOptions{Base: linkBase, Reference: good}, "points outside the repository"},
		{"bad id", TaskInitOptions{ID: "T 1", Base: base, Reference: good}, "task id"},
		{"custom globs that match nothing", TaskInitOptions{Base: base, Reference: good, Tests: []string{"spec/**"}}, "matching spec/**"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.SuiteDir, tc.opts.Repo = suiteDir, r.dir
			if tc.opts.ID == "" {
				tc.opts.ID = "bad"
			}
			_, err := InitTask(context.Background(), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("want error containing %q, got %v", tc.wantError, err)
			}
			if _, err := os.Stat(filepath.Join(suiteDir, "tasks", "bad")); !os.IsNotExist(err) {
				t.Error("a refused task left a folder behind")
			}
		})
	}
	// An existing task folder needs -force.
	if _, err := InitTask(context.Background(), TaskInitOptions{SuiteDir: suiteDir, ID: "ok", Repo: r.dir, Base: noTests, Reference: good}); err != nil {
		t.Fatal(err)
	}
	if _, err := InitTask(context.Background(), TaskInitOptions{SuiteDir: suiteDir, ID: "ok", Repo: r.dir, Base: noTests, Reference: good}); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Fatalf("want -force error, got %v", err)
	}
	if _, err := InitTask(context.Background(), TaskInitOptions{SuiteDir: suiteDir, ID: "ok", Repo: r.dir, Base: noTests, Reference: good, Force: true}); err != nil {
		t.Fatalf("force: %v", err)
	}
	if got := readFile(t, filepath.Join(suiteDir, "suite.yaml")); strings.Count(got, "ok") != 1 {
		t.Errorf("suite.yaml lists the task more than once:\n%s", got)
	}
}

func TestInitTaskUnknownTemplate(t *testing.T) {
	r := newTestRepo(t)
	r.write("main.rb", "x = 1\n")
	r.write("spec/main_spec.rb", "it\n")
	base := r.commit("base")
	r.write("main.rb", "x = 2\n")
	r.write("spec/main_spec.rb", "it it\n")
	ref := r.commit("fix")
	dir := filepath.Join(t.TempDir(), "rb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	suiteYAML := "id: rb\ngrader: verifier_command\nverifier:\n  command: [rspec]\ntasks: []\n"
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(suiteYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := TaskInitOptions{SuiteDir: dir, ID: "r01", Repo: r.dir, Base: base, Reference: ref}
	if _, err := InitTask(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "pass -tests") {
		t.Fatalf("want -tests hint, got %v", err)
	}
	opts.Tests = []string{"spec/**"}
	res, err := InitTask(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, filepath.Join(res.Dir, "verifier/tests/test.sh")), "EDIT ME") {
		t.Error("an unknown template gets the generic test.sh stub")
	}
}

func TestInitTaskWarningsAndExclude(t *testing.T) {
	r := newTestRepo(t)
	r.write("app.py", "X = 1\n")
	r.write("logo.png", "\x89PNG\x00\x01")
	r.write("tests/test_app.py", "def test_x():\n    pass\n")
	r.write("tests/test_old.py", "def test_old():\n    pass\n")
	r.write("bench/benchmarks/py/tasks/t9/verifier/tests/test_secret.py", "def test_s():\n    pass\n")
	r.write("big.dat", strings.Repeat("x", largeFileBytes+1))
	base := r.commit("base")
	r.write("app.py", "X = 2\n")
	r.write("logo.png", "\x89PNG\x00\x02")
	r.write("tests/test_app.py", "def test_x():\n    pass\n\n\ndef test_y():\n    pass\n")
	r.remove("tests/test_old.py")
	r.write("bench/benchmarks/py/notes.md", "changed\n")
	ref := r.commit("fix")
	_, suiteDir := newSuiteDir(t, "py", TemplatePythonPytest)
	res, err := InitTask(context.Background(), TaskInitOptions{
		SuiteDir: suiteDir, ID: "w01", Repo: r.dir, Base: base, Reference: ref, ExcludeDir: filepath.Join(r.dir, "bench"),
	})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"binary files (logo.png)", "big.dat is 5.0 MB", "deletes test file tests/test_old.py", "bench/benchmarks/py/notes.md is in the bench project folder"} {
		if !strings.Contains(all, want) {
			t.Errorf("warnings lack %q:\n%s", want, all)
		}
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "repo", "bench")); !os.IsNotExist(err) {
		t.Error("the bench project folder was copied into repo/")
	}
	if !strings.Contains(readFile(t, filepath.Join(res.Dir, "solution/reference.diff")), "GIT binary patch") {
		t.Error("the reference diff does not carry the binary change")
	}
	if err := suite.ApplyCheck(context.Background(), filepath.Join(res.Dir, "repo"), filepath.Join(res.Dir, "solution/reference.diff")); err != nil {
		t.Errorf("reference diff with a binary file does not apply: %v", err)
	}
}

// TestInitTaskSymlinks checks that a link inside the repository is written
// as a link and validates, and that a chain of links that each look inside
// but climb out together is refused before the task folder appears.
func TestInitTaskSymlinks(t *testing.T) {
	r := newTestRepo(t)
	r.write("app.py", "X = 1\n")
	r.write("docs/guide.md", "guide\n")
	r.write("tests/test_app.py", "def test_x():\n    pass\n")
	if err := os.Symlink("../docs/guide.md", filepath.Join(r.dir, "tests", "guide.md")); err != nil {
		t.Fatal(err)
	}
	base := r.commit("base")
	r.write("app.py", "X = 2\n")
	r.write("tests/test_app.py", "def test_x():\n    pass\n\n\ndef test_y():\n    pass\n")
	good := r.commit("fix")
	if err := os.MkdirAll(filepath.Join(r.dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"a/l": "..", "a/m": "l/.."} {
		if err := os.Symlink(target, filepath.Join(r.dir, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	chainBase := r.commit("links that climb out together")
	r.write("app.py", "X = 3\n")
	r.write("tests/test_app.py", "def test_z():\n    pass\n")
	chainRef := r.commit("fix again")

	benchRoot, suiteDir := newSuiteDir(t, "py", TemplatePythonPytest)
	res, err := InitTask(context.Background(), TaskInitOptions{SuiteDir: suiteDir, ID: "ok", Repo: r.dir, Base: base, Reference: good})
	if err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(res.Dir, "repo", "tests", "guide.md")); err != nil || target != "../docs/guide.md" {
		t.Fatalf("inside link = %q, %v", target, err)
	}
	s, task, err := suite.LoadTask(benchRoot, "py", "ok")
	if err != nil {
		t.Fatalf("a task with an inside link must load: %v", err)
	}
	for _, p := range suite.LintTask(s, task) {
		if strings.Contains(p.Message, "symlink") && p.Severity != suite.SeverityWarning {
			t.Errorf("inside link reported as %s: %s", p.Severity, p.Message)
		}
	}

	_, err = InitTask(context.Background(), TaskInitOptions{SuiteDir: suiteDir, ID: "chain", Repo: r.dir, Base: chainBase, Reference: chainRef})
	if err == nil || !strings.Contains(err.Error(), "symlink a/m points outside the repository") {
		t.Fatalf("want the chain refused, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(suiteDir, "tasks", "chain")); !os.IsNotExist(err) {
		t.Error("a refused task left a folder behind")
	}
}

func TestCheckLink(t *testing.T) {
	tests := []struct {
		path, target string
		ok           bool
	}{
		{"a/link", "../b.txt", true},
		{"a/b/link", "../../c", true},
		{"link", "../outside", false},
		{"a/link", "../../outside", false},
		{"link", "/etc/passwd", false},
		{"link", "", false},
	}
	for _, tc := range tests {
		if err := checkLink(tc.path, tc.target); (err == nil) != tc.ok {
			t.Errorf("checkLink(%s -> %s) = %v", tc.path, tc.target, err)
		}
	}
}

func TestGoImportPath(t *testing.T) {
	mods := map[string]string{".": "example.com/root", "tools/gen": "example.com/gen"}
	tests := map[string]string{
		"a_test.go":               "example.com/root",
		"pkg/x/a_test.go":         "example.com/root/pkg/x",
		"tools/gen/g_test.go":     "example.com/gen",
		"tools/gen/sub/s_test.go": "example.com/gen/sub",
	}
	for p, want := range tests {
		if got, ok := goImportPath(mods, p); !ok || got != want {
			t.Errorf("goImportPath(%s) = %q %v, want %q", p, got, ok, want)
		}
	}
	if _, ok := goImportPath(map[string]string{"sub": "x"}, "other/a_test.go"); ok {
		t.Error("a file with no go.mod above it has an import path")
	}
}

// QC2-21-09: a suite.yaml whose tasks key cannot be edited fails before the
// task is published, and a corrected suite takes a plain retry.
func TestInitTaskSuiteErrorLeavesNoOrphan(t *testing.T) {
	r, base, ref := pythonRepo(t)
	_, suiteDir := newSuiteDir(t, "py", TemplatePythonPytest)
	suitePath := filepath.Join(suiteDir, "suite.yaml")
	good := readFile(t, suitePath)
	if err := os.WriteFile(suitePath, []byte(good+"\ntasks: invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := TaskInitOptions{SuiteDir: suiteDir, ID: "t01", Repo: r.dir, Base: base, Reference: ref, License: "MIT"}
	if _, err := InitTask(context.Background(), opts); err == nil {
		t.Skip("fixture: suite accepted a scalar tasks key")
	}
	if _, err := os.Stat(filepath.Join(suiteDir, "tasks", "t01")); err == nil {
		t.Fatal("a failed init left the task folder")
	}
	if err := os.WriteFile(suitePath, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitTask(context.Background(), opts); err != nil {
		t.Fatalf("retry after fixing the suite: %v", err)
	}
}
