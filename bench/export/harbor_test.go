package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/importers"
	_ "github.com/maximhq/bifrost/bench/importers/harbor"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// codingCore loads the shipped coding-core suite.
func codingCore(t *testing.T) *schemas.Suite {
	t.Helper()
	s, err := suite.Builtin(filepath.Join("..", "benchmarks"))[0].Load(context.Background(), "coding-core")
	if err != nil {
		t.Fatalf("load coding-core: %v", err)
	}
	return s
}

// golden compares got with testdata/<name>, or rewrites it under -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read golden %s (run with -update): %v", p, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file (run with -update to accept)\n--- got\n%s", name, got)
	}
}

func TestHarborRefusesWithoutIncludeHidden(t *testing.T) {
	_, _, err := PlanHarbor(codingCore(t), HarborOptions{})
	if !errors.Is(err, ErrHiddenTests) {
		t.Fatalf("err = %v, want ErrHiddenTests", err)
	}
}

func TestHarborExportCodingCore(t *testing.T) {
	s := codingCore(t)
	trees, res, err := PlanHarbor(s, HarborOptions{IncludeHidden: true, Exporter: "bifrost-bench export harbor test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 0 || len(res.Tasks) != len(s.Tasks) {
		t.Fatalf("tasks %d skipped %+v, want all %d", len(res.Tasks), res.Skipped, len(s.Tasks))
	}
	if len(res.Warnings) == 0 {
		t.Error("an export with hidden tests must warn")
	}
	tree := trees["t01"]
	for _, p := range []string{"task.toml", "instruction.md", "environment/Dockerfile", "environment/repo/ledger/money.py",
		"solution/solve.sh", "solution/patch.diff", "tests/test.sh", "tests/heimdall/check.py", "tests/heimdall/required.txt",
		"tests/heimdall/hidden/test_hidden_rounding.py", "tests/heimdall/hidden/conftest.py", "tests/heimdall/restore/tests/test_money.py", "README.md"} {
		if _, ok := tree[p]; !ok {
			t.Errorf("t01 has no %s", p)
		}
	}
	if tree["tests/test.sh"].mode != 0o755 || tree["solution/solve.sh"].mode != 0o755 {
		t.Error("scripts must be executable")
	}
	golden(t, "harbor/t01/task.toml", tree["task.toml"].data)
	golden(t, "harbor/t01/test.sh", tree["tests/test.sh"].data)
	golden(t, "harbor/t01/solve.sh", tree["solution/solve.sh"].data)
	golden(t, "harbor/t01/Dockerfile", tree["environment/Dockerfile"].data)
	if strings.Contains(string(tree["tests/test.sh"].data), " /grader/") {
		t.Error("test.sh still names /grader")
	}
}

func TestHarborSkipsHeldOutAndUnknown(t *testing.T) {
	s := codingCore(t)
	s.Tasks[1].Split = schemas.SplitHeldOut
	_, res, err := PlanHarbor(s, HarborOptions{IncludeHidden: true, Tasks: []string{"t01", s.Tasks[1].ID, "nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 1 || res.Tasks[0].ID != "t01" {
		t.Fatalf("tasks = %+v", res.Tasks)
	}
	reasons := map[string]string{}
	for _, sk := range res.Skipped {
		reasons[sk.ID] = sk.Reason
	}
	if !strings.Contains(reasons[s.Tasks[1].ID], "held-out") || !strings.Contains(reasons["nope"], "no task") {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
}

func TestHarborSkipsServicesAndJudged(t *testing.T) {
	s := codingCore(t)
	s.Tasks[0].Services = []schemas.ServiceSpec{{ID: "db"}}
	_, res, err := PlanHarbor(s, HarborOptions{IncludeHidden: true, Tasks: []string{"t01"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 0 || !strings.Contains(res.Skipped[0].Reason, "services") {
		t.Fatalf("services task: %+v", res)
	}
	s = codingCore(t)
	s.Grading = schemas.GradingJudged
	_, res, err = PlanHarbor(s, HarborOptions{IncludeHidden: true, Tasks: []string{"t01"}})
	if err != nil || len(res.Tasks) != 0 || !strings.Contains(res.Skipped[0].Reason, "judge") {
		t.Fatalf("judged suite: %+v %v", res, err)
	}
}

func TestHarborWithheld(t *testing.T) {
	trees, res, err := PlanHarbor(codingCore(t), HarborOptions{Withhold: true, Tasks: []string{"t01"}})
	if err != nil {
		t.Fatal(err)
	}
	tree := trees["t01"]
	if !res.Tasks[0].Withheld {
		t.Fatal("not marked withheld")
	}
	for p := range tree {
		if strings.HasPrefix(p, "solution/") || strings.HasPrefix(p, "tests/heimdall/") {
			t.Errorf("withheld export holds %s", p)
		}
	}
	if !strings.Contains(string(tree["task.toml"].data), `heimdall_verifier = "withheld"`) {
		t.Error("task.toml does not say the verifier is withheld")
	}
}

func TestHarborTarIsDeterministic(t *testing.T) {
	s := codingCore(t)
	trees, res, err := PlanHarbor(s, HarborOptions{Withhold: true})
	if err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	if err := WriteHarborTar(&a, "coding-core", trees, res); err != nil {
		t.Fatal(err)
	}
	if err := WriteHarborTar(&b, "coding-core", trees, res); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) || a.Len() == 0 {
		t.Fatal("two tars of the same export differ")
	}
}

// TestHarborRoundTrip exports coding-core and imports it back with the
// harbor importer: every task must import, none skipped.
func TestHarborRoundTrip(t *testing.T) {
	s := codingCore(t)
	trees, res, err := PlanHarbor(s, HarborOptions{IncludeHidden: true, Exporter: "test"})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "harbor")
	if err := WriteHarborDir(out, trees, res, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteHarborDir(out, trees, res, false); err == nil {
		t.Fatal("a second write without force must refuse")
	}
	im, ok := importers.Get("harbor")
	if !ok {
		t.Fatal("harbor importer not registered")
	}
	root := t.TempDir()
	ir, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: out, SuiteID: "cc-back",
		Extra: map[string]string{"benchmark": "coding-core@1.0.0", "source": "heimdall-export", "license": "NOASSERTION"}})
	if err != nil {
		t.Fatalf("import back: %v (skipped %+v)", err, ir)
	}
	if len(ir.Tasks) != len(s.Tasks) || len(ir.Skipped) != 0 {
		t.Fatalf("imported %v, skipped %+v", ir.Tasks, ir.Skipped)
	}
	back, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "cc-back")
	if err != nil {
		t.Fatalf("load the imported suite: %v", err)
	}
	if len(back.Tasks) != len(s.Tasks) {
		t.Fatalf("imported suite has %d tasks", len(back.Tasks))
	}
	// Exporting the imported suite again is refused per task: the Harbor
	// folder it came from is the Harbor task.
	_, again, err := PlanHarbor(back, HarborOptions{IncludeHidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Tasks) != 0 || len(again.Skipped) != len(s.Tasks) || !strings.Contains(again.Skipped[0].Reason, "imported from Harbor") {
		t.Fatalf("re-export of an imported suite: %+v", again)
	}
}

// TestCheckPy runs the exported checker on JUnit reports, when python3 is
// on the machine.
func TestCheckPy(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "check.py")
	if err := os.WriteFile(script, checkPy, 0o644); err != nil {
		t.Fatal(err)
	}
	req := filepath.Join(dir, "required.txt")
	if err := os.WriteFile(req, []byte("test_a.py::test_one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, string) {
		cmd := exec.Command(py, append([]string{"-I", script}, args...)...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("check.py %v: %v %s", args, err, errb.String())
		}
		return strings.TrimSpace(out.String()), errb.String()
	}
	write := func(name, s string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pass := write("pass.xml", `<testsuites><testsuite tests="2" failures="0"><testcase classname="test_a" name="test_one"/><testcase classname="test_a" name="test_two"/></testsuite></testsuites>`)
	fail := write("fail.xml", `<testsuite tests="1"><testcase classname="tests.test_a" name="test_one"><failure/></testcase></testsuite>`)
	missing := write("missing.xml", `<testsuite tests="1"><testcase classname="test_b" name="test_x"/></testsuite>`)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"junit", pass, "0", req}, "1"},
		{[]string{"junit", pass, "1", req}, "0"},
		{[]string{"junit", fail, "1", req}, "0"},
		{[]string{"junit", missing, "0", req}, "0"},
		{[]string{"junit", filepath.Join(dir, "none.xml"), "2", req}, "0"},
		{[]string{"exit_code", "-", "0"}, "1"},
		{[]string{"exit_code", "-", "4"}, "0"},
		{[]string{"reward", write("r.json", `{"reward": 0.5}`), "0", "1"}, "0.5"},
	}
	for _, c := range cases {
		if got, why := run(c.args...); got != c.want {
			t.Errorf("check.py %v = %q (%s), want %q", c.args[:2], got, why, c.want)
		}
	}
	spec := write("answer.json", `{"match":"exact","expected":["Paris"],"normalize":["trim","lower"]}`)
	if got, _ := run("answer", write("a1.txt", "  paris \n"), "0", spec); got != "1" {
		t.Errorf("exact answer = %s, want 1", got)
	}
	if got, _ := run("answer", write("a2.txt", "Lyon"), "0", spec); got != "0" {
		t.Errorf("wrong answer = %s, want 0", got)
	}
	num := write("num.json", `{"match":"numeric","expected":["1,200"],"tolerance":0.5}`)
	for answer, want := range map[string]string{"The cap is 1,200 guests.": "1", "1200.4": "1", "1200 or 1300": "0", "twelve hundred": "0", "none": "0"} {
		if got, why := run("answer", write("n.txt", answer), "0", num); got != want {
			t.Errorf("numeric %q = %s (%s), want %s", answer, got, why, want)
		}
	}
	choice := write("choice.json", `{"match":"choice","expected":["B"]}`)
	for answer, want := range map[string]string{"B": "1", "(b) Paris": "1", "The answer is b.": "1", "A": "0", "C) b": "0"} {
		if got, why := run("answer", write("c.txt", answer), "0", choice); got != want {
			t.Errorf("choice %q = %s (%s), want %s", answer, got, why, want)
		}
	}
}

// TestCheckPyMatchesAnswerMatch runs the same answers through Heimdall's
// answer_match (graders.MatchAnswer) and the exported check.py: they agree.
func TestCheckPyMatchesAnswerMatch(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "check.py")
	if err := os.WriteFile(script, checkPy, 0o644); err != nil {
		t.Fatal(err)
	}
	specs := []schemas.AnswerSpec{
		{Match: "exact", Expected: []string{"Paris"}, Normalize: []string{"trim", "lower", "strip_punct"}},
		{Match: "contains_all", Expected: []string{"blue", "green"}, Normalize: []string{"lower", "collapse_space"}},
		{Match: "regex", Expected: []string{`^\d{4}-\d{2}-\d{2}$`}, Normalize: []string{"trim"}},
		{Match: "numeric", Expected: []string{"42"}, Tolerance: 0.01},
		{Match: "choice", Expected: []string{"C"}},
	}
	answers := []string{"Paris", " paris! ", "PARIS, France", "Blue and GREEN", "green", "2026-11-18", " 2026-11-18\n", "18 Nov",
		"42", "The answer is 42.", "forty-two", "41.995", "42 or 43", "four", "C", "(c) maybe", "answer: c", "c.", "A", "Final answer: C"}
	for i, spec := range specs {
		b, err := json.Marshal(map[string]any{"match": spec.Match, "expected": spec.Expected, "normalize": spec.Normalize, "tolerance": spec.Tolerance})
		if err != nil {
			t.Fatal(err)
		}
		specFile := filepath.Join(dir, fmt.Sprintf("spec%d.json", i))
		if err := os.WriteFile(specFile, b, 0o644); err != nil {
			t.Fatal(err)
		}
		for j, a := range answers {
			af := filepath.Join(dir, fmt.Sprintf("a%d_%d.txt", i, j))
			if err := os.WriteFile(af, []byte(a), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(py, "-I", script, "answer", af, "0", specFile).Output()
			if err != nil {
				t.Fatalf("check.py: %v", err)
			}
			want, _ := graders.MatchAnswer(&spec, a)
			if got := strings.TrimSpace(string(out)) == "1"; got != want {
				t.Errorf("%s %q: check.py %v, answer_match %v", spec.Match, a, got, want)
			}
		}
	}
}
