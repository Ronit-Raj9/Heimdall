package graders

import "github.com/maximhq/bifrost/bench/schemas"

// Builtin lists the graders that ship with the bench. Both share cfg:
// hidden_tests for the Python suites that predate result formats, and
// verifier_command for suites in any language.
func Builtin(cfg HiddenTestsConfig) []schemas.Grader {
	return []schemas.Grader{newHiddenTests(cfg), newVerifierCommand(cfg)}
}
