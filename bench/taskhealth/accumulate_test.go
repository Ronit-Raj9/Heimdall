package taskhealth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestArmClassification(t *testing.T) {
	m := &schemas.Manifest{
		Arms:          map[string]schemas.ArmManifest{"A": {Config: agentA}, "B": {Config: orac}},
		Compatibility: &schemas.ArmManifest{Config: toy},
	}
	tests := []struct {
		arm           string
		smoke         bool
		syn, isOracle bool
	}{
		{"A", false, false, false},
		{"A", true, true, false},
		{"B", false, true, true},
		{compatArm, false, true, false},
		{"missing", false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.arm, func(t *testing.T) {
			m.Experiment.Smoke = tt.smoke
			cfg := armConfig(m, tt.arm)
			if got := synthetic(m, cfg); got != tt.syn {
				t.Fatalf("synthetic = %t, want %t", got, tt.syn)
			}
			if got := oracle(cfg); got != tt.isOracle {
				t.Fatalf("oracle = %t, want %t", got, tt.isOracle)
			}
		})
	}
}

func TestVerdictAndHelpers(t *testing.T) {
	if verdict(&schemas.Grade{Passed: true}) != "pass" || verdict(&schemas.Grade{}) != "fail" ||
		verdict(&schemas.Grade{Passed: true, InvalidEvaluator: true}) != "invalid_evaluator" {
		t.Fatal("verdict")
	}
	if got := capRuns([]string{"a", "b", "c"}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("capRuns = %v", got)
	}
	if short("0123456789abcdef") != "0123456789ab" || short("abc") != "abc" {
		t.Fatal("short")
	}
}

// unreadableSource fails every slot read, so infra counts become a lower
// bound with a note instead of failing the report.
type unreadableSource struct{ *fakeSource }

func (unreadableSource) SlotAttempts(context.Context, string, string) ([]schemas.RunRecord, error) {
	return nil, errors.New("disk gone")
}

func TestUnreadableSlotsAreNoted(t *testing.T) {
	h, err := Compute(context.Background(), unreadableSource{fixture()}, testSuite(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range h.Notes {
		found = found || strings.Contains(n, "infra counts are a lower bound")
	}
	if !found || h.Runs != 0 {
		t.Fatalf("notes %v runs %d", h.Notes, h.Runs)
	}
}

// TestSameOutputAcrossExperiments checks one output graded pass in one
// experiment and fail in another, under the same grader, is evidence that
// names both experiments.
func TestSameOutputAcrossExperiments(t *testing.T) {
	f := newFakeSource()
	f.exp("e1", map[string]schemas.AgentConfig{"A": agentA}).run("easy", "A", schemas.OutcomePass, grade(true, "dup"), 0)
	f.exp("e2", map[string]schemas.AgentConfig{"A": agentB}).run("easy", "A", schemas.OutcomeFail, grade(false, "dup"), 0)
	h, err := Compute(context.Background(), f, testSuite(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ev := taskOf(t, h, "easy").Verifier.Evidence
	if len(ev) != 1 || ev[0].Kind != schemas.EvidenceSameDiffDiffers || !strings.Contains(ev[0].Detail, "across experiments e1, e2") {
		t.Fatalf("evidence %+v", ev)
	}
}
