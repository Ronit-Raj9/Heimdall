package reference

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestCheckRefusesCheatCheck: a cheat check is never published as a score.
func TestCheckRefusesCheatCheck(t *testing.T) {
	err := Check(&schemas.Evaluation{ID: "ev_c", Status: schemas.EvaluationDone, CheatCheckOf: "ev_p"})
	if err == nil || !strings.Contains(err.Error(), "cheat check") {
		t.Fatalf("err = %v", err)
	}
}
