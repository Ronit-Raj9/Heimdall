//go:build integration

package graders

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const testImage = "bench/coding-core-base:1"

func TestMain(m *testing.M) {
	if exec.Command("docker", "image", "inspect", testImage).Run() != nil {
		out, err := exec.Command("docker", "build", "-t", testImage, "../benchmarks/coding-core/base/").CombinedOutput()
		if err != nil {
			os.Stderr.WriteString("integration: build " + testImage + ": " + err.Error() + "\n" + string(out))
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// gradeRC is a run context for the fixture task under the coding-core suite's
// verifier settings.
func gradeRC(t *testing.T, timeoutS int) *schemas.RunContext {
	t.Helper()
	taskDir, err := filepath.Abs("testdata/task")
	if err != nil {
		t.Fatal(err)
	}
	return &schemas.RunContext{
		Run: &schemas.Run{RunID: strings.Replace(utils.NewID("r", 10), "r_", "r_gt", 1), ExpID: "e_gradeit"},
		Suite: &schemas.Suite{
			Version:   "1.0.0",
			BaseImage: schemas.ImageSpec{Tag: testImage},
			Verifier: schemas.SuiteVerifier{
				Command:  []string{"python", "-I", "-B", "-m", "pytest", "-q", "-p", "no:cacheprovider", "--rootdir=/grader", "--junitxml=/grader/out/junit.xml", "/grader/tests"},
				Restore:  []string{"tests", "conftest.py", "pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini", "sitecustomize.py"},
				TimeoutS: timeoutS,
			},
		},
		Task: &schemas.Task{
			ID: "tcalc", Dir: taskDir, RepoDir: "repo",
			Verifier: schemas.TaskVerifier{Tests: "verifier/tests", Required: []string{"test_hidden_calc.py::test_add"}},
		},
		Dir: t.TempDir(),
	}
}

func TestGradePatches(t *testing.T) {
	tests := []struct {
		patch    string
		timeoutS int
		passed   bool
		invalid  bool
		reason   string
		failures int
	}{
		{patch: "pass.diff", passed: true, reason: "passed (2 tests)"},
		{patch: "fail.diff", reason: "exit 1; 1 failures", failures: 1},
		{patch: "empty.diff", reason: "exit 1; 2 failures; required tests failed", failures: 2},
		// A root conftest.py that rewrites failures to passes, plus a padded
		// visible test file: restore and the /grader rootdir make both inert.
		{patch: "exploit.diff", reason: "exit 1; 2 failures", failures: 2},
		{patch: "noapply.diff", reason: ReasonPatchDoesNotApply},
		{patch: "exit.diff", reason: "test run broken by the patch: no junit report"},
		{patch: "hang.diff", timeoutS: 3, reason: "test run broken by the patch: verifier timed out"},
	}
	for _, tt := range tests {
		t.Run(tt.patch, func(t *testing.T) {
			t.Parallel()
			rc := gradeRC(t, tt.timeoutS)
			p := filepath.Join("testdata", "patches", tt.patch)
			sum, err := utils.SHA256File(p)
			if err != nil {
				t.Fatal(err)
			}
			g := newHiddenTests(HiddenTestsConfig{})
			grade, err := g.Grade(context.Background(), rc, &schemas.Diff{Path: p, SHA256: sum})
			if err != nil {
				t.Fatalf("grade: %v", err)
			}
			if grade.Passed != tt.passed || grade.InvalidEvaluator != tt.invalid || !strings.HasPrefix(grade.Reason, tt.reason) || grade.Failures != tt.failures {
				t.Fatalf("grade = %+v", grade)
			}
			if grade.DiffSHA256 != sum || grade.GraderVersion != "1.0.0" || grade.SuiteVersion != "1.0.0" {
				t.Fatalf("grade metadata = %+v", grade)
			}
			assertNoLeftovers(t, rc.Run.RunID)
		})
	}
}

func TestGradeRejectsTamperedDiff(t *testing.T) {
	rc := gradeRC(t, 0)
	p := filepath.Join("testdata", "patches", "pass.diff")
	_, err := newHiddenTests(HiddenTestsConfig{}).Grade(context.Background(), rc, &schemas.Diff{Path: p, SHA256: strings.Repeat("0", 64)})
	if err == nil || !strings.Contains(err.Error(), "export recorded") {
		t.Fatalf("err = %v", err)
	}
	assertNoLeftovers(t, rc.Run.RunID)
}

func assertNoLeftovers(t *testing.T, runID string) {
	t.Helper()
	filter := "label=" + utils.LabelRun + "=" + runID
	for _, args := range [][]string{{"ps", "-aq", "--filter", filter}, {"volume", "ls", "-q", "--filter", filter}} {
		out, err := exec.Command("docker", args...).Output()
		if err != nil {
			t.Fatalf("docker %v: %v", args, err)
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			t.Errorf("leftover docker objects for %s (%s): %s", runID, args[0], s)
		}
	}
}

// nodeImage has Node 22 and no git, for the node --test junit suite: the
// helper image prepares the workspace. The test skips when the image is not
// on the host rather than pulling it.
const nodeImage = "node:22-slim"

// vcCase is one verifier_command grade of a fixture task.
type vcCase struct {
	name     string
	patch    string
	tests    string // the task's hidden verifier folder
	workdir  string
	v        schemas.SuiteVerifier
	required []string
	passed   bool
	reason   string // prefix of the wanted reason
	failures int
	reward   *float64
	dims     map[string]float64
}

// vcRC is a run context for a fixture task graded by verifier_command.
func vcRC(t *testing.T, taskDir, image string, c vcCase) *schemas.RunContext {
	t.Helper()
	dir, err := filepath.Abs(taskDir)
	if err != nil {
		t.Fatal(err)
	}
	return &schemas.RunContext{
		Run: &schemas.Run{RunID: strings.Replace(utils.NewID("r", 10), "r_", "r_vc", 1), ExpID: "e_gradeit"},
		Suite: &schemas.Suite{
			Version: "1.0.0", Grader: VerifierCommandType, Workdir: c.workdir,
			BaseImage: schemas.ImageSpec{Tag: image},
			Verifier:  c.v,
		},
		Task: &schemas.Task{
			ID: "tvc", Dir: dir, RepoDir: "repo",
			Verifier: schemas.TaskVerifier{Tests: c.tests, Required: c.required},
		},
		Dir: t.TempDir(),
	}
}

// runVC grades one case and checks the grade, its metadata and that no
// docker object is left behind.
func runVC(t *testing.T, g *verifierCommand, taskDir, image string, c vcCase) {
	t.Helper()
	rc := vcRC(t, taskDir, image, c)
	p := filepath.Join("testdata", "patches", c.patch)
	sum, err := utils.SHA256File(p)
	if err != nil {
		t.Fatal(err)
	}
	grade, err := g.Grade(context.Background(), rc, &schemas.Diff{Path: p, SHA256: sum})
	if err != nil {
		t.Fatalf("grade: %v", err)
	}
	format := c.v.Result
	if format == "" {
		format = schemas.VerifierResultJUnit
	}
	if grade.Passed != c.passed || grade.InvalidEvaluator || !strings.HasPrefix(grade.Reason, c.reason) || grade.Failures != c.failures {
		t.Fatalf("grade = %+v", grade)
	}
	if !reflect.DeepEqual(grade.Reward, c.reward) || !reflect.DeepEqual(grade.Dimensions, c.dims) {
		t.Fatalf("reward = %v, dimensions = %v, want %v, %v", grade.Reward, grade.Dimensions, c.reward, c.dims)
	}
	if grade.ResultFormat != format || grade.DiffSHA256 != sum || grade.GraderVersion != "1.0.0" || grade.SuiteVersion != "1.0.0" {
		t.Fatalf("grade metadata = %+v", grade)
	}
	assertNoLeftovers(t, rc.Run.RunID)
}

func fptr(v float64) *float64 { return &v }

func TestVerifierCommandFormats(t *testing.T) {
	restore := []string{"tests", "conftest.py", "pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini", "sitecustomize.py"}
	pytest := schemas.SuiteVerifier{
		Command: []string{"python", "-I", "-B", "-m", "pytest", "-q", "-p", "no:cacheprovider", "--rootdir=/grader", "--junitxml=/grader/out/report.xml", "/grader/tests"},
		Restore: restore, Result: schemas.VerifierResultJUnit, ResultPath: "/grader/out/report.xml",
	}
	reward := func(timeoutS int, threshold float64, command ...string) schemas.SuiteVerifier {
		if len(command) == 0 {
			command = []string{"sh", "/grader/tests/test.sh"}
		}
		return schemas.SuiteVerifier{Command: command, Restore: restore, Result: schemas.VerifierResultReward, PassThreshold: threshold, TimeoutS: timeoutS}
	}
	exitCode := func(timeoutS int, command ...string) schemas.SuiteVerifier {
		if len(command) == 0 {
			command = []string{"sh", "/grader/tests/check.sh"}
		}
		return schemas.SuiteVerifier{Command: command, Restore: restore, Result: schemas.VerifierResultExitCode, TimeoutS: timeoutS}
	}
	wrongPath := pytest
	wrongPath.ResultPath = "/grader/out/junit.xml"
	harborTxt := reward(0, 0.5, "sh", "-c", "head -c 50000000 /dev/zero; echo 0.5 > /grader/out/reward.txt")
	harborTxt.ResultPath = "/grader/out/reward.txt"
	full := map[string]float64{"correctness": 1, "cases_passed": 4}
	partial := map[string]float64{"correctness": 0.75, "cases_passed": 3}
	quarter := map[string]float64{"correctness": 0.25, "cases_passed": 1}
	req := []string{"test_hidden_calc.py::test_add"}
	cases := []vcCase{
		{name: "junit pass", patch: "pass.diff", tests: "verifier/tests", v: pytest, required: req, passed: true, reason: "passed (2 tests)"},
		{name: "junit fail", patch: "fail.diff", tests: "verifier/tests", v: pytest, required: req, reason: "exit 1; 1 failures", failures: 1},
		{name: "junit exploit stays inert", patch: "exploit.diff", tests: "verifier/tests", v: pytest, required: req, reason: "exit 1; 2 failures", failures: 2},
		{name: "junit at the wrong path is a broken run", patch: "pass.diff", tests: "verifier/tests", v: wrongPath, reason: "test run broken by the patch: no junit report"},
		{name: "reward pass", patch: "pass.diff", tests: "verifier/reward", v: reward(0, 0), passed: true, reason: "passed (reward 1)", reward: fptr(1), dims: full},
		{name: "reward below threshold", patch: "fail.diff", tests: "verifier/reward", v: reward(0, 0), reason: "reward 0.75 below pass threshold 1", reward: fptr(0.75), dims: partial},
		{name: "reward above a lower threshold", patch: "fail.diff", tests: "verifier/reward", v: reward(0, 0.5), passed: true, reason: "passed (reward 0.75)", reward: fptr(0.75), dims: partial},
		{name: "reward empty diff", patch: "empty.diff", tests: "verifier/reward", v: reward(0, 0), reason: "reward 0.25 below", reward: fptr(0.25), dims: quarter},
		{name: "reward files in the workspace are ignored", patch: "fakereward.diff", tests: "verifier/reward", v: reward(0, 0), reason: "reward 0.25 below", reward: fptr(0.25), dims: quarter},
		{name: "reward planted by agent code is overwritten", patch: "plant.diff", tests: "verifier/reward", v: reward(20, 0), reason: "reward 0.25 below", reward: fptr(0.25), dims: quarter},
		{name: "diff escaping the workspace does not apply", patch: "traversal.diff", tests: "verifier/reward", v: reward(0, 0), reason: ReasonPatchDoesNotApply},
		{name: "reward timeout", patch: "hang.diff", tests: "verifier/reward", v: reward(3, 0), reason: "test run broken by the patch: verifier timed out"},
		{name: "reward symlink into the workspace is no file", patch: "fakereward.diff", tests: "verifier/reward",
			v: reward(0, 0, "sh", "-c", "ln -s /workspace/reward.json /grader/out/reward.json"), reason: "test run broken by the patch: no reward file"},
		{name: "reward folder at the result path is no file", patch: "pass.diff", tests: "verifier/reward",
			v: reward(0, 0, "sh", "-c", "mkdir /grader/out/reward.json && head -c 3000000 /dev/urandom > /grader/out/reward.json/big"), reason: "test run broken by the patch: no reward file"},
		{name: "reward oversized file is a broken run", patch: "pass.diff", tests: "verifier/reward",
			v: reward(0, 0, "sh", "-c", "head -c 100000 /dev/zero | tr '\\0' 1 > /grader/out/reward.json"), reason: "test run broken by the patch: reward file is 100000 bytes"},
		{name: "reward nan is a broken run", patch: "pass.diff", tests: "verifier/reward",
			v: reward(0, 0, "sh", "-c", "echo NaN > /grader/out/reward.json"), reason: `test run broken by the patch: reward "NaN" is not a decimal number`},
		{name: "harbor reward.txt after huge output", patch: "pass.diff", tests: "verifier/reward", v: harborTxt, passed: true, reason: "passed (reward 0.5)", reward: fptr(0.5)},
		{name: "exit code pass", patch: "pass.diff", tests: "verifier/exitcode", v: exitCode(0), passed: true, reason: "passed (exit 0)"},
		{name: "exit code fail", patch: "fail.diff", tests: "verifier/exitcode", v: exitCode(0), reason: "exit 1"},
		{name: "exit code with fake files fails", patch: "fakereward.diff", tests: "verifier/exitcode", v: exitCode(0), reason: "exit 1"},
		{name: "exit code patch does not apply", patch: "noapply.diff", tests: "verifier/exitcode", v: exitCode(0), reason: ReasonPatchDoesNotApply},
		{name: "exit code timeout", patch: "hang.diff", tests: "verifier/exitcode", v: exitCode(3), reason: "test run broken by the patch: verifier timed out"},
		// exit_code trusts the command: agent code that exits 0 while the
		// check imports it passes. docs/verifiers.md says to prefer junit or
		// reward when the command runs agent code; junit catches this one.
		{name: "exit code trusts the command", patch: "exit.diff", tests: "verifier/exitcode", v: exitCode(0), passed: true, reason: "passed (exit 0)"},
		{name: "junit catches the early exit", patch: "exit.diff", tests: "verifier/tests", v: pytest, required: req, reason: "test run broken by the patch: no junit report"},
		{name: "workspace mounted at the suite workdir", patch: "pass.diff", tests: "verifier/exitcode", workdir: "/srv/app",
			v: exitCode(0, "sh", "-c", `test "$(pwd)" = /srv/app && test -f /srv/app/calc/__init__.py && test ! -e /workspace/calc`), passed: true, reason: "passed (exit 0)"},
	}
	g := newVerifierCommand(HiddenTestsConfig{})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			runVC(t, g, "testdata/task", testImage, c)
		})
	}
}

// TestVerifierCommandNode grades a Node suite: the trusted helper prepares
// the workspace and the verifier runs on a different image, with Node's
// built-in junit reporter and its top-level testcases.
func TestVerifierCommandNode(t *testing.T) {
	if exec.Command("docker", "image", "inspect", nodeImage).Run() != nil {
		t.Skipf("%s is not on this host", nodeImage)
	}
	v := schemas.SuiteVerifier{
		Command: []string{"node", "--test", "--test-reporter=junit", "--test-reporter-destination=/grader/out/junit.xml", "/grader/tests/**/*.test.js"},
		Result:  schemas.VerifierResultJUnit,
	}
	cases := []vcCase{
		{name: "node pass", patch: "nodepass.diff", tests: "verifier/tests", v: v, required: []string{"test::adds"}, passed: true, reason: "passed (2 tests)"},
		{name: "node fail", patch: "empty.diff", tests: "verifier/tests", v: v, required: []string{"test::adds"}, reason: "exit 1; 2 failures", failures: 2},
	}
	g := newVerifierCommand(HiddenTestsConfig{Image: testImage})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			runVC(t, g, "testdata/nodetask", nodeImage, c)
		})
	}
}
