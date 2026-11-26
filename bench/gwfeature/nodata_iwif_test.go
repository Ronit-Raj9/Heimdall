package gwfeature

import (
	"context"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The owner's rule for every Bifrost feature (group F integration): a
// result always names its source and, without enough data, says so with an
// explicit unknown or not enough data state and a reason. These tests feed
// every registered feature the three thin cases: no runs, too few tasks,
// and gateway rows without the sealed raw detail.

// nodataInput is a feature test over arms A and B with the given runs.
func nodataInput(feature string, runs []Run) Input {
	m := &schemas.Manifest{ExpID: "e_thin", Arms: map[string]schemas.ArmManifest{
		"A": {Label: "today"}, "B": {Label: "changed"}, "C": {Label: "other"},
	}}
	test := schemas.GatewayTest{Feature: feature, Control: "A", Variants: []string{"B", "C"}, Options: map[string]string{}}
	m.Experiment.GatewayTest = &test
	return Input{ExpID: "e_thin", Manifest: m, Test: test, Runs: runs}
}

// thinRuns is one task with a run per arm; withRows adds message rows
// whose raw detail was not sealed.
func thinRuns(withRows bool) []Run {
	var out []Run
	ok := true
	cost := 0.01
	for i, arm := range []string{"A", "B", "C"} {
		r := Run{RunID: fmt.Sprintf("r%d", i), Task: "t1", Arm: arm, Condition: schemas.ConditionClean, Trial: 1, Success: &ok, CostUSD: &cost, RowsComplete: withRows}
		if withRows {
			for j := 0; j < 3; j++ {
				r.Rows = append(r.Rows, Row{ID: fmt.Sprintf("%s-%d", r.RunID, j), Kind: "message", Status: "success", Provider: "anthropic", ModelAsked: "m1", LatencyMS: 100})
			}
		}
		out = append(out, r)
	}
	return out
}

var thinStates = map[string]bool{StateNotEnoughData: true, StateNotApplied: true, StateUnknown: true}

func TestEveryFeatureSaysUnknownOrNotEnoughDataOnThinData(t *testing.T) {
	cases := map[string][]Run{"no runs": nil, "one task, no gateway rows": thinRuns(false), "rows without raw detail": thinRuns(true)}
	n := 0
	for _, f := range Features() {
		if f.Compute == nil || len(f.ID) < 3 || !isProductFeature(f.ID) {
			continue
		}
		n++
		for name, runs := range cases {
			t.Run(f.ID+"/"+name, func(t *testing.T) {
				var e Effect
				func() {
					defer func() {
						if p := recover(); p != nil {
							t.Fatalf("Compute panicked: %v", p)
						}
					}()
					e = f.Compute(context.Background(), nodataInput(f.ID, runs))
				}()
				if !thinStates[e.State] {
					t.Fatalf("state %q, want unknown, not_enough_data or not_applied (headline %q)", e.State, e.Headline)
				}
				if e.Reason == "" || e.Headline == "" {
					t.Fatalf("state %s without a reason or headline: %+v", e.State, e)
				}
			})
		}
	}
	if n != 8 {
		t.Fatalf("%d product features registered, want 8", n)
	}
}

// isProductFeature is true for the feature ids of schemas/gatewaytest.go
// (test files of other packages register their own fakes).
func isProductFeature(id string) bool {
	switch id {
	case schemas.GatewayFeatureComplexityRouter, schemas.GatewayFeaturePromptVersion, schemas.GatewayFeatureSkillVersion,
		schemas.GatewayFeatureSemanticCache, schemas.GatewayFeatureMCPTools, schemas.GatewayFeatureProviders,
		schemas.GatewayFeatureBudgetCurve, schemas.GatewayFeatureOverhead:
		return true
	}
	return false
}

// TestComputeUnknownNamesItsSource: the unknown answer of Compute (no runs
// yet) still says where it looked.
func TestComputeUnknownNamesItsSource(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_empty", Arms: map[string]schemas.ArmManifest{"A": {}, "B": {}}}
	m.Experiment.GatewayTest = &schemas.GatewayTest{Feature: schemas.GatewayFeatureSemanticCache, Control: "A", Variants: []string{"B"}}
	src := &fakeSource{manifests: map[string]*schemas.Manifest{"e_empty": m}}
	e, err := Compute(context.Background(), src, t.TempDir(), "e_empty", false)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != StateUnknown || e.Reason == "" || e.Source.Text == "" || e.Source.ExpID != "e_empty" {
		t.Fatalf("effect %+v", e)
	}
}
