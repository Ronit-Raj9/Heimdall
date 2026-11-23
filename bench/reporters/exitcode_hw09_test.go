package reporters

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestExitCodeLeader: a leader among three or more setups names no release
// candidate, so a check that fails on it exits like an inconclusive verdict,
// never like a regression (fix wave 3, N244, D12).
func TestExitCodeLeader(t *testing.T) {
	if got := ExitCode(schemas.DecisionLeader, []schemas.Decision{schemas.DecisionLeader}); got != ExitInconclusive {
		t.Fatalf("exit %d, want %d", got, ExitInconclusive)
	}
	if got := ExitCode(schemas.DecisionLeader, []schemas.Decision{schemas.DecisionRegressed, schemas.DecisionInvalid}); got != ExitOK {
		t.Fatalf("exit %d, want 0 when leader is not listed", got)
	}
}
