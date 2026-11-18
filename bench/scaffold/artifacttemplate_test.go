package scaffold

import (
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// checkArtifactShape checks what the artifact template's tasks declare: a
// verifier_command suite with output kind artifact, required artifacts in
// the task, and script controls.
func checkArtifactShape(t *testing.T, s *schemas.Suite, task *schemas.Task) {
	t.Helper()
	if s.Output == nil || s.Output.Kind != schemas.OutputArtifact || s.Grader != "verifier_command" || s.Verifier.Result != schemas.VerifierResultReward ||
		s.Track != schemas.TrackArtifact {
		t.Errorf("suite output %+v grader %s result %s track %s", s.Output, s.Grader, s.Verifier.Result, s.Track)
	}
	specs := utils.OutputArtifacts(s, task)
	if len(specs) != 2 || specs[0].ID != "report" || specs[1].ID != "chart" || !specs[0].Required || !specs[1].Required {
		t.Errorf("artifacts = %+v", specs)
	}
	if len(task.Services) != 0 || len(task.Sessions) != 0 {
		t.Errorf("services %+v sessions %+v", task.Services, task.Sessions)
	}
	if !strings.HasSuffix(task.Controls.Reference, ".sh") || !strings.HasSuffix(task.Controls.Empty, ".sh") {
		t.Errorf("controls = %+v, want scripts", task.Controls)
	}
}

// artifactMissing is the reward RunArtifactControl returns when a required
// artifact was not written: the bench fails such a run without running the
// verifier.
const artifactMissing = -1

// RunArtifactControl runs one control script of an artifact task on the
// host, the way admission runs it in the agent's place: in a fresh copy of
// the task's workspace. It then copies each declared artifact to
// <grader>/<id>/<base name>, as the bench's export does, runs the task's
// verify.py on that folder and returns the reward, or artifactMissing when a
// required artifact matched no file.
func RunArtifactControl(t *testing.T, s *schemas.Suite, task *schemas.Task, script string) float64 {
	t.Helper()
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	if err := os.CopyFS(ws, os.DirFS(filepath.Join(task.Dir, filepath.FromSlash(task.RepoDir)))); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(task.Dir, filepath.FromSlash(script)))
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "HOME="+tmp)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", script, err, b)
	}
	grader := filepath.Join(tmp, "artifacts")
	for _, a := range utils.OutputArtifacts(s, task) {
		matches, err := filepath.Glob(filepath.Join(ws, filepath.FromSlash(a.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 && a.Required {
			return artifactMissing
		}
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(grader, a.ID, path.Base(filepath.ToSlash(m)))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, b, 0o444); err != nil {
				t.Fatal(err)
			}
		}
	}
	out := filepath.Join(tmp, "out")
	verify := exec.Command("python3", "-I", "-B", filepath.Join(task.Dir, filepath.FromSlash(task.Verifier.Tests), "verify.py"))
	verify.Env = append(os.Environ(), "GRADER_ARTIFACTS="+grader, "GRADER_OUT="+out)
	if b, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("verify.py after %s: %v\n%s", script, err, b)
	}
	var res struct {
		Reward     *float64       `json:"reward"`
		Dimensions map[string]any `json:"dimensions"`
	}
	b, err := os.ReadFile(filepath.Join(out, "reward.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Strict, as the verifier_command grader is: only reward and dimensions.
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil || res.Reward == nil {
		t.Fatalf("reward.json after %s: %v: %s", script, err, b)
	}
	return *res.Reward
}

// artifactControlWant is what admission requires of each control of an
// artifact task: the reference passes, every other control fails, either in
// the verifier or before it (a required artifact missing).
func artifactControlWant(task *schemas.Task) map[string]bool {
	want := map[string]bool{task.Controls.Reference: true, task.Controls.Empty: false, task.Controls.Exploit: false}
	for _, m := range task.Controls.Mutants {
		want[m] = false
	}
	return want
}

func checkArtifactControls(t *testing.T, s *schemas.Suite, task *schemas.Task) {
	t.Helper()
	for script, pass := range artifactControlWant(task) {
		got := RunArtifactControl(t, s, task, script)
		if (got == 1) != pass {
			t.Errorf("%s: reward %v, want pass %v", script, got, pass)
		}
	}
}

// TestScenarioArtifactControls runs the artifact template's sample controls
// on the host: the reference reaches reward 1, the empty control leaves the
// required files missing, and the mutant and the exploit reach reward 0.
func TestScenarioArtifactControls(t *testing.T) {
	if _, err := lookPython(); err != nil {
		t.Skip("python3 not installed")
	}
	root, _ := newSuiteDir(t, "demo", TemplateArtifact)
	s, task, err := suite.LoadTask(root, "demo", scenarioSamples[TemplateArtifact])
	if err != nil {
		t.Fatal(err)
	}
	if got := RunArtifactControl(t, s, task, task.Controls.Empty); got != artifactMissing {
		t.Errorf("empty control: reward %v, want the required artifacts missing", got)
	}
	for _, m := range task.Controls.Mutants {
		if got := RunArtifactControl(t, s, task, m); got != 0 {
			t.Errorf("%s: reward %v, want 0", m, got)
		}
	}
	checkArtifactControls(t, s, task)
}

// TestExampleArtifactControls does the same for every artifact task of the
// example projects.
func TestExampleArtifactControls(t *testing.T) {
	if _, err := lookPython(); err != nil {
		t.Skip("python3 not installed")
	}
	ran := 0
	for dir, tmpl := range exampleSuites(t) {
		if tmpl != TemplateArtifact {
			continue
		}
		var list struct {
			Tasks []string `yaml:"tasks"`
		}
		if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(dir, "suite.yaml"))), &list); err != nil {
			t.Fatal(err)
		}
		for _, id := range list.Tasks {
			s, task, err := suite.LoadTask(filepath.Dir(dir), filepath.Base(dir), id)
			if err != nil {
				t.Fatalf("load %s/%s: %v", dir, id, err)
			}
			if utils.OutputKind(s, task) != schemas.OutputArtifact {
				continue
			}
			ran++
			if s.Grader == "visual" { // X1 vision grading: the verifier's side only (visualtask_test.go)
				t.Run(s.ID+"/"+task.ID, func(t *testing.T) { checkVisualArtifactControls(t, s, task) })
				continue
			}
			t.Run(s.ID+"/"+task.ID, func(t *testing.T) { checkArtifactControls(t, s, task) })
		}
	}
	if ran == 0 {
		t.Skip("no artifact example tasks")
	}
}

// TestInitArtifactTask: task init in an artifact suite copies the sample's
// shape, keeps its artifacts, and marks the instruction, the verifier and
// the reference script.
func TestInitArtifactTask(t *testing.T) {
	root, dir := newSuiteDir(t, "demo", TemplateArtifact)
	res, err := InitScenarioTask(ScenarioTaskOptions{SuiteDir: dir, ID: "t02", Kind: TemplateArtifact})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"instruction.md", "verifier/tests/verify.py", "solution/reference.sh", "verifier/requirements.md"} {
		found := false
		for _, rel := range res.EditMe {
			found = found || rel == want
		}
		if !found {
			t.Errorf("EDIT ME files %v miss %s", res.EditMe, want)
		}
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "controls", "mutant-refunds-counted.sh")); err == nil {
		t.Error("the mutant was copied")
	}
	s, task, err := suite.LoadTask(root, "demo", "t02")
	if err != nil {
		t.Fatal(err)
	}
	checkArtifactShape(t, s, task)
	if !strings.Contains(readFile(t, filepath.Join(dir, "suite.yaml")), "tasks: [sales-report, t02]") {
		t.Errorf("suite.yaml tasks not updated:\n%s", readFile(t, filepath.Join(dir, "suite.yaml")))
	}
	if _, err := InitScenarioTask(ScenarioTaskOptions{SuiteDir: dir, ID: "t03", Kind: TemplateQA}); err == nil ||
		!strings.Contains(err.Error(), "written from template artifact") {
		t.Errorf("a -kind that contradicts the suite: %v", err)
	}
}

// TestDetectArtifactTemplate: a suite.yaml without its "# template:" line is
// still recognised as an artifact suite by its track.
func TestDetectArtifactTemplate(t *testing.T) {
	if got := DetectTemplate([]byte("id: s\ntrack: artifact\ngrader: verifier_command\n")); got != TemplateArtifact {
		t.Errorf("DetectTemplate = %q, want %s", got, TemplateArtifact)
	}
}
