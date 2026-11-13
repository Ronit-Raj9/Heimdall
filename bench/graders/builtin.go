package graders

import "github.com/maximhq/bifrost/bench/schemas"

// Builtin lists the graders that ship with the bench.
func Builtin(cfg HiddenTestsConfig) []schemas.Grader {
	return []schemas.Grader{newHiddenTests(cfg)}
}
