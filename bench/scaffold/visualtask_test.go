package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/visual"
)

// X1 vision grading: task init -visual.

func TestInitScenarioTaskVisual(t *testing.T) {
	for tmpl, kind := range map[string]string{TemplateArtifact: schemas.VisualCaptureArtifact, "browser": schemas.VisualCaptureBrowser, "computer-use": schemas.VisualCaptureDesktop} {
		t.Run(tmpl, func(t *testing.T) {
			root, dir := newSuiteDir(t, "demo", tmpl)
			if _, err := InitScenarioTask(ScenarioTaskOptions{SuiteDir: dir, ID: "looks", Visual: true}); err != nil {
				t.Fatal(err)
			}
			s, task, err := suite.LoadTask(root, "demo", "looks")
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if s.Grader != visual.GraderName {
				t.Fatalf("suite grader %s, want visual", s.Grader)
			}
			v := task.Output.Visual
			if v == nil || v.Capture.Kind != kind || v.Reference != "verifier/reference.png" {
				t.Fatalf("output.visual = %+v", v)
			}
			raw := readFile(t, filepath.Join(dir, "tasks", "looks", "task.yaml"))
			if !strings.Contains(raw, "EDIT ME") || !strings.Contains(raw, "bifrost-bench visual") {
				t.Errorf("task.yaml has no EDIT ME note on the reference:\n%s", raw)
			}
			// The reference does not exist yet: validation refuses the task
			// until the author makes one.
			errs := strings.Join(editMeErrors(s, task), "\n")
			if !strings.Contains(errs, "output.visual.reference") {
				t.Fatalf("validation errors %q do not name the missing reference", errs)
			}
			// The sample task, without output.visual, is still graded by the
			// verifier command alone and lints clean on grader visual.
			ss, sample, err := suite.LoadTask(root, "demo", scenarioSamples[tmpl])
			if err != nil {
				t.Fatalf("load sample: %v", err)
			}
			for _, p := range suite.ValidateTask(t.Context(), ss, sample) {
				if strings.Contains(p.Message, "visual") {
					t.Errorf("sample task: %s", p.Message)
				}
			}
		})
	}
	_, dir := newSuiteDir(t, "qa", TemplateQA)
	if _, err := InitScenarioTask(ScenarioTaskOptions{SuiteDir: dir, ID: "x", Visual: true}); err == nil || !strings.Contains(err.Error(), "-visual applies to") {
		t.Fatalf("qa -visual: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", "x")); err == nil {
		t.Fatal("a refused -visual wrote the task folder")
	}
}

// checkVisualArtifactControls runs a visual artifact task's controls on the
// host against its verifier alone: the reference passes, the exploit
// fails, and the empty control and every mutant pass or leave the files
// missing, because a visual task's mutants are wrong only in how they look,
// which the capture decides (suite admit renders them and fails them).
func checkVisualArtifactControls(t *testing.T, s *schemas.Suite, task *schemas.Task) {
	t.Helper()
	if got := RunArtifactControl(t, s, task, task.Controls.Reference); got != 1 {
		t.Errorf("reference: reward %v, want 1", got)
	}
	// The empty control leaves the files missing, unless the workspace
	// starts with them (a page to restyle): then the verifier passes them
	// and only the looks are wrong.
	wantEmpty := float64(artifactMissing)
	for _, a := range utils.OutputArtifacts(s, task) {
		if m, _ := filepath.Glob(filepath.Join(task.Dir, filepath.FromSlash(task.RepoDir), filepath.FromSlash(a.Path))); len(m) > 0 {
			wantEmpty = 1
		}
	}
	if got := RunArtifactControl(t, s, task, task.Controls.Empty); got != wantEmpty {
		t.Errorf("empty control: reward %v, want %v", got, wantEmpty)
	}
	if got := RunArtifactControl(t, s, task, task.Controls.Exploit); got == 1 {
		t.Errorf("exploit: reward 1, want a fail")
	}
	for _, m := range task.Controls.Mutants {
		if got := RunArtifactControl(t, s, task, m); got != 1 {
			t.Errorf("%s: reward %v from the verifier, want 1 (only its looks are wrong)", m, got)
		}
	}
}
