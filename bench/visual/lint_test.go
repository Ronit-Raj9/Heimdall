package visual

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func lintFixture(t *testing.T) (*schemas.Suite, *schemas.Task) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "verifier"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	png, _ := NewImage(200, 120, [3]uint8{255, 255, 255}).EncodePNG()
	for _, p := range []string{"verifier/reference.png", "repo/leak.png"} {
		if err := os.WriteFile(filepath.Join(dir, p), png, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "verifier", "rubric.md"), []byte("- blue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &schemas.Suite{ID: "s", Grader: GraderName}
	task := &schemas.Task{ID: "t", Dir: dir, RepoDir: "repo", Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact,
		Artifacts: []schemas.ArtifactSpec{{ID: "chart", Path: "chart.svg"}, {ID: "many", Path: "out/*.png"}}}}
	return s, task
}

func messages(ps []LintProblem, sev string) string {
	var out []string
	for _, p := range ps {
		if p.Severity == sev {
			out = append(out, p.Message)
		}
	}
	return strings.Join(out, "\n")
}

func TestLintAcceptsAWellFormedCheck(t *testing.T) {
	s, task := lintFixture(t)
	v := &schemas.VisualCheck{
		Capture:   schemas.VisualCapture{Kind: schemas.VisualCaptureArtifact, Artifact: "chart", Width: 200, Height: 120},
		Reference: "verifier/reference.png", Method: schemas.VisualMethodPixel, Threshold: 0.01, Tolerance: 8,
		Masks: []schemas.VisualRect{{X: 0, Y: 0, W: 20, H: 10}},
		Checks: []schemas.VisualAssert{
			{Method: schemas.VisualMethodColors, Colors: []string{"#2563eb"}, Area: &schemas.VisualRect{X: 10, Y: 10, W: 50, H: 50}},
			{Method: schemas.VisualMethodRegion, Crop: &schemas.VisualRect{X: 10, Y: 10, W: 40, H: 20}, At: &schemas.VisualPoint{X: 10, Y: 10}},
			{Method: schemas.VisualMethodPHash, Threshold: 10},
		},
		Rubric: "verifier/rubric.md",
		Judge:  &schemas.VisualJudgeSpec{Provider: "anthropic", Model: "m", PromptVersion: PromptVisualV1, Votes: 3},
	}
	s.Grading = schemas.GradingJudged
	if ps := Lint(s, task, v); len(ps) != 0 {
		t.Fatalf("problems: %+v", ps)
	}
}

func TestLintRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*schemas.Suite, *schemas.Task, *schemas.VisualCheck)
		want string
	}{
		{"wrong grader", func(s *schemas.Suite, _ *schemas.Task, _ *schemas.VisualCheck) { s.Grader = "verifier_command" }, "read only by grader visual"},
		{"reference missing", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Reference = "verifier/nope.png" }, "is not a file of the task folder"},
		{"reference outside", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Reference = "../x.png" }, "stay inside"},
		{"reference the agent sees", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Reference = "repo/leak.png" }, "inside repo_dir"},
		{"mask outside the image", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Masks = []schemas.VisualRect{{X: 190, Y: 0, W: 20, H: 10}}
		}, "not inside the 200x120 image"},
		{"empty mask", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Masks = []schemas.VisualRect{{X: 1, Y: 1}}
		}, "empty or negative"},
		{"ssim threshold over 1", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Method, v.Threshold = schemas.VisualMethodSSIM, 1.5
		}, "outside 0 to 1"},
		{"phash threshold over 64", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Method, v.Threshold = schemas.VisualMethodPHash, 70
		}, "outside 0 to 64"},
		{"tolerance out of range", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Tolerance = 300 }, "tolerance 300"},
		{"unknown method", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Method = "ocr" }, "is not dimensions"},
		{"no reference for pixel", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Reference = "" }, "needs output.visual.reference"},
		{"bad colour", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Checks = []schemas.VisualAssert{{Method: schemas.VisualMethodColors, Colors: []string{"blue"}}}
		}, "is not #rrggbb"},
		{"crop outside the reference", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Checks = []schemas.VisualAssert{{Method: schemas.VisualMethodRegion, Crop: &schemas.VisualRect{X: 180, Y: 0, W: 40, H: 10}}}
		}, "not inside the 200x120 image"},
		{"region without crop", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Checks = []schemas.VisualAssert{{Method: schemas.VisualMethodRegion}}
		}, "needs crop"},
		{"duplicate ids", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Checks = []schemas.VisualAssert{{ID: "a", Method: schemas.VisualMethodPHash}, {ID: "a", Method: schemas.VisualMethodPHash}}
		}, "used by an earlier check"},
		{"unknown artifact", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Capture.Artifact = "nope" }, "is not an artifact"},
		{"glob without file", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Capture.Artifact = "many" }, "is a glob"},
		{"artifact capture on a state task", func(_ *schemas.Suite, t *schemas.Task, _ *schemas.VisualCheck) { t.Output.Kind = schemas.OutputState }, "needs output kind artifact"},
		{"browser without service", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Capture = schemas.VisualCapture{Kind: schemas.VisualCaptureBrowser, URL: "http://webapp:8000/"}
		}, `no service "browser"`},
		{"browser without url", func(_ *schemas.Suite, t *schemas.Task, v *schemas.VisualCheck) {
			t.Services = []schemas.ServiceSpec{{ID: "browser", Port: 8931}}
			v.Capture = schemas.VisualCapture{Kind: schemas.VisualCaptureBrowser}
		}, "must be the http url"},
		{"desktop without port", func(_ *schemas.Suite, t *schemas.Task, v *schemas.VisualCheck) {
			t.Services = []schemas.ServiceSpec{{ID: "desktop"}}
			v.Capture = schemas.VisualCapture{Kind: schemas.VisualCaptureDesktop}
		}, "has no port"},
		{"bad scale", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Capture.Scale = 5 }, "scale 5"},
		{"rubric without judge", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) { v.Rubric = "verifier/rubric.md" }, "needs judge"},
		{"judge not pinned", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Rubric, v.Judge = "verifier/rubric.md", &schemas.VisualJudgeSpec{Provider: "anthropic", Model: "pin-me", PromptVersion: PromptVisualV1}
		}, "pin-me"},
		{"rubric in a verified suite", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Rubric, v.Judge = "verifier/rubric.md", &schemas.VisualJudgeSpec{Provider: "anthropic", Model: "m", PromptVersion: PromptVisualV1}
		}, "must declare grading: judged"},
		{"nothing to grade", func(_ *schemas.Suite, _ *schemas.Task, v *schemas.VisualCheck) {
			v.Method, v.Threshold, v.Tolerance = "", 0, 0
		}, "nothing would be graded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, task := lintFixture(t)
			v := &schemas.VisualCheck{
				Capture:   schemas.VisualCapture{Kind: schemas.VisualCaptureArtifact, Artifact: "chart", Width: 200, Height: 120},
				Reference: "verifier/reference.png", Method: schemas.VisualMethodPixel,
			}
			tc.edit(s, task, v)
			if got := messages(Lint(s, task, v), LintError); !strings.Contains(got, tc.want) {
				t.Fatalf("errors %q, want one containing %q", got, tc.want)
			}
		})
	}
}
