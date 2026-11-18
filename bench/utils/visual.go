package utils

import "github.com/maximhq/bifrost/bench/schemas"

// VisualJudged reports whether a suite's vision judge decides outcomes: the
// suite's or any task's output.visual names a rubric (X1 vision grading,
// docs/visual-grading.md). Such a suite declares grading: judged and is
// judged by the rubric's pinned vision judge, so it needs no suite judge.
func VisualJudged(s *schemas.Suite) bool {
	if s == nil {
		return false
	}
	if s.Output != nil && s.Output.Visual != nil && s.Output.Visual.Rubric != "" {
		return true
	}
	for _, t := range s.Tasks {
		if t != nil && t.Output != nil && t.Output.Visual != nil && t.Output.Visual.Rubric != "" {
			return true
		}
	}
	return false
}
