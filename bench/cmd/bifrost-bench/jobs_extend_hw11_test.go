package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Extend on a comparison whose test was broken is refused before it
// spends: a follow-up would be broken the same way (fix wave 3, N246).
func TestExtendRefusesInvalidParent(t *testing.T) {
	a, _ := jobsProject(t)
	ctx := context.Background()
	env := jobEnv{App: a}
	params := json.RawMessage(`{"exp_id":"e_demo","trials":1,"force":true}`)
	// No analysis yet: accepted (the run analyses first).
	if _, _, err := validateExtendJob(ctx, env, params); err != nil {
		t.Fatalf("no analysis: %v", err)
	}
	an := &schemas.Analysis{AnalysisID: "a_broken1", ExpID: "e_demo", GradeSource: schemas.GradeSourceOriginal, Verdict: schemas.Verdict{
		Decision:       schemas.DecisionInvalid,
		InvalidReasons: []string{"gate model_fidelity fired in 3 of 3 runs (100.0%) of arm B, above 5%", "second reason"},
	}}
	if err := a.store.WriteAnalysis(ctx, an, nil); err != nil {
		t.Fatal(err)
	}
	want := "This comparison's test was broken (gate model_fidelity fired in 3 of 3 runs (100.0%) of arm B, above 5%). Fix that first; a follow-up would be broken the same way."
	for _, raw := range []string{`{"exp_id":"e_demo","trials":1,"force":true}`, `{"exp_id":"e_demo","tasks":2}`} {
		_, _, err := validateExtendJob(ctx, env, json.RawMessage(raw))
		if !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want a 400 with %q", raw, err, want)
		}
	}
}
