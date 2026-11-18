package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X1 vision grading: the app registers exactly one visual grader, the wired
// one, so admission (which takes the last grader of a name) and the engine
// (the first) both use it.
func TestWithGraderReplacesBuiltinVisual(t *testing.T) {
	wired := graders.NewVisual(graders.VisualConfig{})
	list := withGrader(graders.Builtin(graders.HiddenTestsConfig{}), wired)
	n := 0
	var first, last schemas.Grader
	for _, g := range list {
		if g.GetName() == graders.VisualType {
			n++
			if first == nil {
				first = g
			}
			last = g
		}
	}
	if n != 1 || first != wired || last != wired {
		t.Fatalf("%d visual graders; first is wired %v, last is wired %v", n, first == wired, last == wired)
	}
	if len(list) != len(graders.Builtin(graders.HiddenTestsConfig{})) {
		t.Fatalf("other graders lost: %d", len(list))
	}
}
