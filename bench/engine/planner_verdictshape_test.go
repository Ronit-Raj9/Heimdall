package engine

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A fixed paired experiment whose arms are not A and B is refused before
// any slot runs (audit 1-B-05).
func TestValidateExperimentPairedNeedsArmsAB(t *testing.T) {
	e := testExperiment()
	e.Arms = map[string]schemas.ArmSpec{"baseline": e.Arms["A"], "candidate": e.Arms["B"]}
	err := validateExperiment(e, 2)
	if err == nil || !strings.Contains(err.Error(), "needs arms A (baseline) and B (candidate)") {
		t.Fatalf("err = %v", err)
	}
	if err := validateExperiment(testExperiment(), 2); err != nil {
		t.Fatalf("A/B experiment refused: %v", err)
	}
	if err := validateExperiment(e, 1); err != nil && strings.Contains(err.Error(), "needs arms A") {
		t.Fatalf("agent check must not need A and B: %v", err)
	}
}

// A fault-only primary with no guards would get the default clean guard and
// read INVALID; the planner refuses it, and accepts it once guards are
// declared (audit 1-B-08).
func TestValidateExperimentDefaultGuardNeedsClean(t *testing.T) {
	e := testExperiment(schemas.ConditionSpec{ID: "tool_errors", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1})
	err := validateExperiment(e, 2)
	if err == nil || !strings.Contains(err.Error(), "default guard checks the clean condition") {
		t.Fatalf("err = %v", err)
	}
	e.Guards = []schemas.GuardSpec{{Rule: GuardNoNewIntegrityViolation}}
	if err := validateExperiment(e, 2); err != nil {
		t.Fatalf("explicit guards refused: %v", err)
	}
}
