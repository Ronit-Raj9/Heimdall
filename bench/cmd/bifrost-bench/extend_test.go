package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// TestExperimentYAMLRoundTrip checks that the follow-up `extend` writes is
// read back by the strict decoder and the experiment schema.
func TestExperimentYAMLRoundTrip(t *testing.T) {
	e := &schemas.Experiment{
		Name: "codewins-ext", Suite: "coding-core@1.0.0", Base: "agents/claude-code.yaml",
		Arms:       map[string]schemas.ArmSpec{"A": {Label: "baseline"}, "B": {Label: "note", Set: map[string]any{"notes.user_file": "experiments/fixtures/codewins.md"}}},
		Primary:    schemas.Endpoint{Condition: "clean", Metric: "verified_success"},
		Guards:     []schemas.GuardSpec{{Condition: "clean", Rule: "no_regression"}},
		Conditions: []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t05", "t07"}}, Trials: 2}},
		Verdict:    schemas.VerdictSpec{Policy: "paired_signflip", Alpha: 0.05, AssumedSD: 0.3},
		Seed:       20261119,
		Extends:    &schemas.ExtendsRef{ExpID: "e_first", ManifestHash: "abc", Kind: "tasks", Rule: schemas.ExtensionInverseNormal},
	}
	raw, err := experimentYAML(e)
	if err != nil {
		t.Fatal(err)
	}
	var back schemas.Experiment
	if err := suite.DecodeYAML(raw, "ext.yaml", suite.SchemaExperiment, &back); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if back.Extends == nil || back.Extends.ExpID != "e_first" || strings.Join(back.Conditions[0].Tasks.IDs, ",") != "t05,t07" || back.Seed != 20261119 || back.Conditions[0].Trials != 2 {
		t.Fatalf("round trip %+v", back)
	}
}

func TestAdaptiveSection(t *testing.T) {
	cost := 3.5
	an := &schemas.Analysis{Verdict: schemas.Verdict{Tier: "pr",
		Adaptive:     &schemas.AdaptiveReport{Rule: schemas.AdaptiveTask, MaxSlots: 48, RequiredSlots: 16, RunSlots: 16, RunsSaved: 32, StopReason: "concordant", DecisionAlpha: 0.05},
		MoreEvidence: &schemas.MoreEvidence{TargetPP: 10, Tasks: 52, TasksTotal: 60, SD: 0.3, SDAssumed: true, Runs: 312, CostUSD: &cost, CostBasis: "mean", Combination: schemas.ExtensionExploratory, Command: "heimdall extend -exp e -tasks 52"}}}
	got := adaptiveSection(an, "# Report\n")
	for _, want := range []string{"## Trials and tier", "Tier: `pr`", "32 not required (runs saved)", "to detect 10.0 pp you need about 52 more tasks", "estimated cost 3.50 USD", "exploratory"} {
		if !strings.Contains(got, want) {
			t.Fatalf("section lacks %q:\n%s", want, got)
		}
	}
	if adaptiveSection(&schemas.Analysis{}, "x") != "x" {
		t.Fatal("a fixed analysis got a section")
	}
}
