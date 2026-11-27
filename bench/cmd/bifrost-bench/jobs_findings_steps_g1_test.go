package main

import "testing"

// A run with no steps.jsonl gives the investigation its steps from
// evidence.json, as steprules.Load does for the Behaviour checks.
func TestInvestigateStepsFallBackToEvidence(t *testing.T) {
	env := w442Fixture(t)
	idx := env.Services.Index.(*w442Index)
	for k := range idx.files {
		delete(idx.files, k)
	}
	idx.files["r3/evidence.json"] = []byte(`{"steps":[{"seq":2,"kind":"tool"},{"seq":1,"kind":"model"}]}`)
	in := investigateInputs(env, investigateParams{ExpID: "e1", MaxRuns: 10})
	if len(in) == 0 || in[0].RunID != "r3" || len(in[0].Steps) != 2 || in[0].Steps[0].Seq != 1 {
		t.Fatalf("inputs %+v", in)
	}
}
