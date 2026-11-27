package engine

import (
	"fmt"
	"testing"
)

func TestFailureCause(t *testing.T) {
	cases := map[string]string{
		"plan e_x: arm A: policy refused: base image ghcr.io/x/base:1 is not in the shared worker's image registry; ask the operator to copy it there (deploy/hosted/registry-sync.sh ghcr.io/x/base:1)": CauseImageMissing,
		"no runner claimed the job within 2m0s: every runner is busy": CauseNoWorker,
		"no runner online":               CauseNoWorker,
		"refused: the runner has no GPU": CauseWorkerRefused,
		"the agent timed out":            "",
		"":                               "",
	}
	for msg, want := range cases {
		if got := FailureCause(msg); got != want {
			t.Errorf("FailureCause(%q) = %q, want %q", msg, got, want)
		}
	}
	if got := FailureCauseOf(fmt.Errorf("dispatch: %w", ErrNoRunner)); got != CauseNoWorker {
		t.Fatalf("wrapped ErrNoRunner: %q", got)
	}
	if FailureCauseOf(nil) != "" {
		t.Fatal("nil error has a cause")
	}
}

// TestNoRunnerTextMatchesErr: schemas.FailureCause knows ErrNoRunner's text.
func TestNoRunnerTextMatchesErr(t *testing.T) {
	if got := FailureCause("dispatch: " + ErrNoRunner.Error()); got != CauseNoWorker {
		t.Fatalf("ErrNoRunner text reads %q", got)
	}
}
