package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// TestTaskInitKindMismatchKeepsFlagHint checks that the command line still
// tells its user which flag to pass when the task type does not match
// (wave 4, N78: the plain error carries no flag, the command line adds it).
func TestTaskInitKindMismatchKeepsFlagHint(t *testing.T) {
	err := &scaffold.KindMismatchError{Suite: "b/suite.yaml", Detected: "qa", Wanted: "tool-use"}
	got := scenarioTaskInitError(err)
	if !strings.Contains(got, "pass -kind qa or leave -kind out") || !strings.Contains(got, "b/suite.yaml") {
		t.Fatalf("got %q", got)
	}
	if other := scenarioTaskInitError(errors.New("boom")); other != "task init: boom" {
		t.Fatalf("other error = %q", other)
	}
}
