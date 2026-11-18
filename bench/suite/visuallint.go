package suite

import (
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/visual"
)

// X1 vision grading (docs/visual-grading.md): lint of output.visual, the
// rules themselves in visual.Lint.

// lintVisualTask reports a task's output.visual problems against task.yaml.
func lintVisualTask(s *schemas.Suite, t *schemas.Task) []Problem {
	taskYAML := rel(s.Root, filepath.Join(t.Dir, TaskFile))
	v := visualSpec(s, t)
	if v == nil {
		// Grader visual grades a task without output.visual by the suite's
		// verifier command alone, as verifier_command would.
		return nil
	}
	var out []Problem
	for _, p := range visual.Lint(s, t, v) {
		out = append(out, Problem{Path: taskYAML, Severity: p.Severity, Message: p.Message})
	}
	return out
}

// visualReadsArtifacts reports whether the grader reads artifact files:
// verifier_command from /grader/artifacts, visual through its capture.
func visualReadsArtifacts(grader string) bool { return grader == visual.GraderName }

func visualSpec(s *schemas.Suite, t *schemas.Task) *schemas.VisualCheck {
	if t != nil && t.Output != nil && t.Output.Visual != nil {
		return t.Output.Visual
	}
	if s.Output != nil {
		return s.Output.Visual
	}
	return nil
}
