package suite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// X1 vision grading: the example project lints clean, and the lint hook
// reports a broken output.visual against task.yaml.
func TestVisualExampleLintsClean(t *testing.T) {
	root := filepath.Join("..", "examples", "acme-visual", "benchmarks")
	s, err := Builtin(root)[0].Load(context.Background(), "acme-visual")
	if err != nil {
		t.Fatalf("load acme-visual: %v", err)
	}
	if len(s.Tasks) != 3 {
		t.Fatalf("%d tasks, want 3", len(s.Tasks))
	}
	for _, p := range Lint(s) {
		if p.Severity == SeverityError {
			t.Errorf("lint %s: %s", p.Path, p.Message)
		}
	}
	for _, task := range s.Tasks {
		for _, p := range ValidateTask(context.Background(), s, task) {
			if p.Severity == SeverityError {
				t.Errorf("%s: %s: %s", task.ID, p.Path, p.Message)
			}
		}
		if visualSpec(s, task) == nil {
			t.Errorf("%s has no output.visual", task.ID)
		}
	}
	// Break one task's spec: the problem names task.yaml.
	task := s.TaskByID("revenue-chart")
	task.Output.Visual.Reference = "repo/chart-spec.md"
	var found bool
	for _, p := range lintVisualTask(s, task) {
		if p.Severity == SeverityError && strings.HasSuffix(p.Path, "task.yaml") && strings.Contains(p.Message, "output.visual.reference") {
			found = true
		}
	}
	if !found {
		t.Fatal("a reference inside repo_dir that is not an image was not reported")
	}
}
