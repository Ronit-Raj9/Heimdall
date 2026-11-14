package scaffold

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

func TestEditMeMarkerMatchesSuite(t *testing.T) {
	if EditMeMarker != suite.EditMeMarker {
		t.Fatalf("scaffold marker %q, suite marker %q", EditMeMarker, suite.EditMeMarker)
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"tests/**", "tests/test_a.py", true},
		{"tests/**", "tests/unit/deep/test_a.py", true},
		{"tests/**", "pkg/tests/test_a.py", false},
		{"tests/**", "tests", false},
		{"test_*.py", "pkg/sub/test_a.py", true},
		{"test_*.py", "pkg/a_test.py", false},
		{"*_test.py", "a_test.py", true},
		{"**/*_test.go", "calc_test.go", true},
		{"**/*_test.go", "internal/calc/calc_test.go", true},
		{"**/*_test.go", "internal/calc/calc.go", false},
		{"**/*.test.js", "src/deep/a.test.js", true},
		{"test/**", "test/helpers/x.js", true},
		{"./test/**", "test/x.js", true},
		{"src/*.js", "src/a/b.js", false},
		{"", "a", false},
	}
	for _, tc := range tests {
		if got := MatchGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
	if !MatchAny(DefaultTestGlobs(TemplatePythonPytest), "lib/test_x.py") || MatchAny(DefaultTestGlobs(TemplatePythonPytest), "lib/x.py") {
		t.Error("python defaults")
	}
	if DefaultTestGlobs("unknown") != nil {
		t.Error("unknown template has default globs")
	}
}

func TestDetectTemplate(t *testing.T) {
	tests := []struct {
		yaml string
		want string
	}{
		{"# template: go-test\nid: x\n", TemplateGoTest},
		{"# template: cobol\ngrader: hidden_tests\n", TemplatePythonPytest},
		{"grader: hidden_tests\n", TemplatePythonPytest},
		{"grader: verifier_command\nverifier:\n  command: [sh, -c, \"go test -json ./... | go-junit\"]\n", TemplateGoTest},
		{"grader: verifier_command\nverifier:\n  command: [node, --test]\n", TemplateNodeTest},
		{"grader: verifier_command\nverifier:\n  command: [sh, /grader/tests/test.sh]\n  result: reward\n", TemplateGenericReward},
		{"grader: verifier_command\nverifier:\n  command: [make, check]\n", ""},
		{":\n- [", ""},
	}
	for _, tc := range tests {
		if got := DetectTemplate([]byte(tc.yaml)); got != tc.want {
			t.Errorf("DetectTemplate(%q) = %q, want %q", tc.yaml, got, tc.want)
		}
	}
}

func TestAddTaskID(t *testing.T) {
	tests := []struct {
		name, in, want string
		changed        bool
	}{
		{"empty flow", "id: s\ntasks: []\nprobes: []\n", "id: s\ntasks: [t09]\nprobes: []\n", true},
		{"flow with comment", "tasks: [t01, t02] # keep\nprobes: []\n", "tasks: [t01, t02, t09] # keep\nprobes: []\n", true},
		{"multi-line flow with trailing comma", "tasks: [\n  t01,\n  t02,\n]\n", "tasks: [\n  t01,\n  t02, t09,\n]\n", true},
		{"block", "tasks:\n  - t01\n  # second\n  - t02\nprobes: []\n", "tasks:\n  - t01\n  # second\n  - t02\n  - t09\nprobes: []\n", true},
		{"block at end without newline", "tasks:\n- t01", "tasks:\n- t01\n- t09\n", true},
		{"empty key", "tasks:\nprobes: []\n", "tasks: [t09]\nprobes: []\n", true},
		{"null", "tasks: ~\n", "tasks: [t09]\n", true},
		{"missing key", "id: s", "id: s\ntasks: [t09]\n", true},
		{"already listed", "tasks: [t09]\n", "tasks: [t09]\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, err := AddTaskID([]byte(tc.in), "t09")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want || changed != tc.changed {
				t.Fatalf("got %q (changed %v), want %q", got, changed, tc.want)
			}
		})
	}
	for _, bad := range []string{"tasks: {a: b}\n", "- a\n", "tasks: [t01 # note\n]\n"} {
		if _, _, err := AddTaskID([]byte(bad), "t09"); err == nil {
			t.Errorf("AddTaskID(%q): want error", bad)
		}
	}
}

func TestPythonTestIDs(t *testing.T) {
	src := `import pytest


def helper():
    def test_nested():
        pass


def test_top():
    def test_inner():
        pass


@pytest.mark.parametrize("x", [1, 2])
async def test_async(x):
    pass


class TestThing:
    def test_method(self):
        pass

    async def test_other(self):
        def test_deep():
            pass


class Helper:
    def test_not_collected(self):
        pass


def test_last():
    pass
`
	got := PythonTestIDs("tests/test_x.py", []byte(src))
	want := []string{
		"tests/test_x.py::test_top", "tests/test_x.py::test_async",
		"tests/test_x.py::TestThing::test_method", "tests/test_x.py::TestThing::test_other",
		"tests/test_x.py::test_last",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestGoTestIDs(t *testing.T) {
	src := "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {}\nfunc TestMain(m *testing.M) {}\nfunc Testlower(t *testing.T) {}\nfunc Test(t *testing.T) {}\nfunc BenchmarkAdd(b *testing.B) {}\nfunc TestSub(tt *testing.T) {}\n"
	got := GoTestIDs("example.com/m/calc", []byte(src))
	want := []string{"example.com/m/calc::TestAdd", "example.com/m/calc::Test", "example.com/m/calc::TestSub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	if mp := GoModulePath([]byte("// x\nmodule \"example.com/m\"\n\ngo 1.22\n")); mp != "example.com/m" {
		t.Fatalf("module path = %q", mp)
	}
	if GoModulePath([]byte("go 1.22\n")) != "" {
		t.Fatal("module path without a module line")
	}
}

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		tmpl, path string
		want       bool
	}{
		{TemplatePythonPytest, "tests/test_a.py", true},
		{TemplatePythonPytest, "tests/conftest.py", false},
		{TemplateGoTest, "a/b_test.go", true},
		{TemplateGoTest, "a/b.go", false},
		{TemplateNodeTest, "src/a.test.ts", true},
		{TemplateNodeTest, "test/helpers.js", true},
		{TemplateNodeTest, "test/data.json", false},
		{TemplateNodeTest, "src/a.js", false},
		{TemplateGenericReward, "tests/test.sh", false},
	}
	for _, tc := range tests {
		if got := IsTestFile(tc.tmpl, tc.path); got != tc.want {
			t.Errorf("IsTestFile(%s, %s) = %v", tc.tmpl, tc.path, got)
		}
	}
}

func TestSplitMessage(t *testing.T) {
	subject, body := SplitMessage("Fix rounding\r\n\r\nHalf cents rounded down.\nSecond line.\n\nSigned-off-by: A <a@x>\nCo-authored-by: B <b@x>\n")
	if subject != "Fix rounding" || body != "Half cents rounded down.\nSecond line." {
		t.Fatalf("subject %q body %q", subject, body)
	}
	if s, b := SplitMessage(""); s != "" || b != "" {
		t.Fatalf("empty message: %q %q", s, b)
	}
}

func TestSanitizeSource(t *testing.T) {
	tests := map[string]string{
		"https://user:ghp_secret@github.com/org/app.git": "https://github.com/org/app.git",
		"git@github.com:org/app.git":                     "git@github.com:org/app.git",
		"/home/me/src/app":                               "/home/me/src/app",
		"ssh://git@host/org/app":                         "ssh://host/org/app",
	}
	for in, want := range tests {
		if got := SanitizeSource(in); got != want {
			t.Errorf("SanitizeSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaskYAML(t *testing.T) {
	meta := TaskMeta{
		ID: "t01", Title: `Fix "quoted" title: yes`, Project: "app",
		Required: []string{"tests/test_a.py::test_x"}, Protected: []string{"tests/test_a.py"},
		Capabilities: []string{"test_discipline"}, Difficulty: "hard",
		Source: "https://example.com/app.git", Base: strings.Repeat("a", 40), Reference: strings.Repeat("b", 40),
	}
	raw, err := TaskYAML(meta)
	if err != nil {
		t.Fatal(err)
	}
	var task schemas.Task
	if err := suite.DecodeYAML(raw, "task.yaml", suite.SchemaTask, &task); err != nil {
		t.Fatalf("task.yaml: %v\n%s", err, raw)
	}
	p := task.Provenance
	if task.Title != meta.Title || task.Kind != "bugfix" || task.Difficulty != "hard" || p == nil || p.License != "proprietary" ||
		p.CreatedBy != "task init" || p.BaseCommit != meta.Base || task.Controls.Exploit != "controls/exploit.sh" || len(task.Controls.Mutants) != 0 {
		t.Fatalf("decoded %+v %+v", task, p)
	}
	meta.Required, meta.Protected, meta.Capabilities, meta.Difficulty = nil, nil, nil, ""
	raw, err = TaskYAML(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := suite.DecodeYAML(raw, "task.yaml", suite.SchemaTask, &schemas.Task{}); err != nil {
		t.Fatalf("minimal task.yaml: %v\n%s", err, raw)
	}
	for _, bad := range []TaskMeta{{ID: "T 1"}, {ID: "t1", Kind: "chore"}, {ID: "t1", Difficulty: "brutal"}} {
		if _, err := TaskYAML(bad); err == nil {
			t.Errorf("TaskYAML(%+v): want error", bad)
		}
	}
}

func TestInstruction(t *testing.T) {
	b, err := Instruction("Fix it", "Body line.", strings.Repeat("c", 40))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, EditMeMarker+"\n") || !strings.Contains(s, "# Fix it\n\nBody line.") || !strings.Contains(s, "cccccccccccc") {
		t.Fatalf("instruction = %q", s)
	}
	for _, term := range []string{"hidden test", "grader", "agents.md", "claude.md"} {
		if strings.Contains(strings.ToLower(s), term) {
			t.Errorf("draft instruction mentions %q, which lint refuses", term)
		}
	}
}

func TestTaskControls(t *testing.T) {
	want := map[string][]string{
		TemplatePythonPytest:  {"controls/empty.diff", "controls/exploit.sh", "verifier/tests/conftest.py"},
		TemplateNodeTest:      {"controls/empty.diff", "controls/exploit.sh"},
		TemplateGoTest:        {"controls/empty.diff", "controls/exploit.sh"},
		TemplateGenericReward: {"controls/empty.diff", "controls/exploit.sh", "verifier/tests/test.sh"},
	}
	for tmpl, paths := range want {
		files, err := TaskControls(tmpl)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range files {
			got = append(got, f.Path)
			if strings.HasSuffix(f.Path, ".sh") && f.Mode != 0o755 {
				t.Errorf("%s %s mode %o", tmpl, f.Path, f.Mode)
			}
			if f.Path == "controls/empty.diff" && len(f.Data) != 0 {
				t.Errorf("%s empty.diff is not empty", tmpl)
			}
		}
		if !reflect.DeepEqual(got, paths) {
			t.Errorf("%s controls = %v, want %v", tmpl, got, paths)
		}
	}
	if _, err := TaskControls("cobol"); err == nil {
		t.Error("want error for an unknown template")
	}
}
