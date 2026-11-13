//go:build integration

package graders

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
