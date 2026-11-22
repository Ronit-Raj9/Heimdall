package graders

import "github.com/maximhq/bifrost/bench/schemas"

// Builtin lists the graders that ship with the bench. Both share cfg:
// hidden_tests for the Python suites that predate result formats, and
// verifier_command for suites in any language.
func Builtin(cfg HiddenTestsConfig) []schemas.Grader {
	return []schemas.Grader{newHiddenTests(cfg), newVerifierCommand(cfg), newAnswerMatch(), newJudge(JudgeConfig{Logger: cfg.Logger}), // answer_match, judge: answer tasks (unwired judge; app registers NewJudge first)
		newVisual(VisualConfig{Inner: cfg, Logger: cfg.Logger}), // X1 vision grading: visual (judge unwired; app registers NewVisual first)
		newChecks(ChecksConfig{Inner: cfg, Logger: cfg.Logger})} // declarative checks.yaml (judge unwired; app registers NewChecks first)
}
