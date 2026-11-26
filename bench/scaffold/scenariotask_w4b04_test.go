package scaffold

import (
	"errors"
	"strings"
	"testing"
)

// TestScenarioKindMismatchPlain checks that a task type that does not match
// the benchmark is a typed error with plain text and no command line flag
// (wave 4, F1.3 and N78).
func TestScenarioKindMismatchPlain(t *testing.T) {
	_, err := scenarioKind("qa", "tool-use", "/srv/b/suite.yaml")
	var km *KindMismatchError
	if !errors.As(err, &km) {
		t.Fatalf("err = %v, want *KindMismatchError", err)
	}
	if km.Detected != "qa" || km.Wanted != "tool-use" || km.Suite != "/srv/b/suite.yaml" {
		t.Fatalf("fields = %+v", km)
	}
	want := "This benchmark holds qa tasks, not tool-use tasks. Choose qa or leave the task type on Automatic."
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), "-kind") {
		t.Fatalf("Error() names a flag: %q", err.Error())
	}
}
