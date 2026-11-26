package twins

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

func task(t *testing.T, files map[string]string) *schemas.Task {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "src", "t1")
	files["task.yaml"] = "id: t1\ninstruction: do it\nverifier:\n  tests: verifier/tests\ncontrols:\n  reference: controls/reference.diff\n"
	files["controls/reference.diff"] = "diff\n"
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &schemas.Task{ID: "t1", Dir: dir, Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}, Controls: schemas.TaskControls{Reference: "controls/reference.diff"}}
}

func makeOne(t *testing.T, files map[string]string) (*Mutation, string) {
	t.Helper()
	src := task(t, files)
	dst := t.TempDir()
	m, err := Make(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	return m, filepath.Join(dst, "tasks", "t1-twin")
}

func TestMakePythonIntLiteral(t *testing.T) {
	m, dir := makeOne(t, map[string]string{"verifier/tests/test_a.py": "def test_a():\n    assert add(1, 2) == 3\n"})
	if m.Rule != "python_assert_eq" || m.Line != 2 || !strings.HasSuffix(m.After, "== 4") || m.File != "verifier/tests/test_a.py" {
		t.Fatalf("mutation %+v", m)
	}
	b, _ := os.ReadFile(filepath.Join(dir, m.File))
	if !strings.Contains(string(b), "== 4") {
		t.Fatalf("twin test %s", b)
	}
	o, _ := os.ReadFile(filepath.Join(dir, OriginalTestsDir, "test_a.py"))
	if !strings.Contains(string(o), "== 3") {
		t.Fatalf("original tests %s", o)
	}
}

func TestMakeJSToBe(t *testing.T) {
	m, _ := makeOne(t, map[string]string{"verifier/tests/a.test.ts": "it('x', () => {\n  expect(fmt(1)).toBe(\"one\");\n});\n"})
	if m.Rule != "js_expect" || !strings.Contains(m.After, `toBe("one_x")`) {
		t.Fatalf("mutation %+v", m)
	}
}

func TestMakeGoWant(t *testing.T) {
	m, _ := makeOne(t, map[string]string{"verifier/tests/a_test.go": "package a\n\nfunc TestA(t *testing.T) {\n\twant := 7\n\t_ = want\n}\n"})
	if m.Rule != "go_want" || !strings.Contains(m.After, "want := 8") {
		t.Fatalf("mutation %+v", m)
	}
}

func TestMakeSkipsComments(t *testing.T) {
	m, _ := makeOne(t, map[string]string{"verifier/tests/test_a.py": "# assert x == 1\ndef test_a():\n    assert y == 5\n"})
	if m.Line != 3 || !strings.Contains(m.After, "== 6") {
		t.Fatalf("mutation %+v", m)
	}
}

func TestMakeNoTestsDir(t *testing.T) {
	src := task(t, map[string]string{})
	if _, err := Make(src, t.TempDir()); !errors.Is(err, ErrNoTests) {
		t.Fatalf("err %v", err)
	}
	src.Verifier.Tests = ""
	if _, err := Make(src, t.TempDir()); !errors.Is(err, ErrNoTests) || err.Error() != "This kind of task has no test file to change." {
		t.Fatalf("err %v", err)
	}
	src = task(t, map[string]string{"verifier/tests/test_a.py": "def test_a():\n    assert ok()\n"})
	if _, err := Make(src, t.TempDir()); !errors.Is(err, ErrNoMutation) {
		t.Fatalf("err %v", err)
	}
}

func TestTwinSetsImpossibleAndHonest(t *testing.T) {
	_, dir := makeOne(t, map[string]string{"verifier/tests/test_a.py": "def test_a():\n    assert f() == \"a\"\n"})
	b, err := os.ReadFile(filepath.Join(dir, "task.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var got schemas.Task
	if err := yaml.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "t1-twin" || !got.Impossible || got.Verifier.OriginalTests != OriginalTestsDir || got.Controls.Honest != "controls/reference.diff" || got.Instruction != "do it" {
		t.Fatalf("task %+v", got)
	}
}

func TestMakeSuiteWritesTwinBenchmark(t *testing.T) {
	a := task(t, map[string]string{"verifier/tests/test_a.py": "def test_a():\n    assert f() == 1\n"})
	b := &schemas.Task{ID: "t2", Dir: t.TempDir()}
	srcRoot := filepath.Dir(a.Dir)
	if err := os.WriteFile(filepath.Join(srcRoot, "suite.yaml"), []byte("id: s\nversion: \"1\"\ngrader: hidden_tests\ntasks: [t1, t2]\nprobes: [p1]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bench := t.TempDir()
	res, err := MakeSuite(&schemas.Suite{ID: "s", Root: srcRoot, Tasks: []*schemas.Task{a, b}}, bench, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Suite != "s-twins" || len(res.Made) != 1 || len(res.Skipped) != 1 || res.Skipped[0].Reason != ErrNoTests.Error() {
		t.Fatalf("result %+v", res)
	}
	raw, _ := os.ReadFile(filepath.Join(bench, "s-twins", "suite.yaml"))
	var s schemas.Suite
	if err := yaml.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.ID != "s-twins" || s.Grader != "hidden_tests" || len(s.TaskIDs) != 1 || s.TaskIDs[0] != "t1-twin" || len(s.ProbeIDs) != 0 {
		t.Fatalf("suite %+v", s)
	}
}
