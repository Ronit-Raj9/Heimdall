package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestInvestigateReadsFinalAnswer: each run's final answer (the claim the
// run recorded) reaches the investigation, redacted before the prompt.
func TestInvestigateReadsFinalAnswer(t *testing.T) {
	env := w442Fixture(t)
	idx := env.Services.Index.(*w442Index)
	idx.runs[2].Run.Claim = &schemas.Claim{Text: "All done, I used the key sk-abcdefghijklmnopqrstuvwx123456 to log in."}
	in := investigateInputs(env, investigateParams{ExpID: "e1", MaxRuns: 10})
	if len(in) != 2 || in[0].RunID != "r3" || !strings.Contains(in[0].Final, "All done") || in[1].Final != "" {
		t.Fatalf("inputs %+v", in)
	}
	fake := &investigator.FakeModel{Replies: []string{`{"items":[]}`, `{"items":[]}`, `{"groups":[]}`}, CostUSD: 0.01, Cap: 1}
	old := openCaller
	openCaller = func(context.Context, jobEnv, *jobs.Job, modelParams) (investigator.Caller, func(), error) {
		return fake, func() {}, nil
	}
	t.Cleanup(func() { openCaller = old })
	j := &jobs.Job{ID: "job_2", Params: json.RawMessage(`{"exp_id":"e1","provider":"openai","model":"m","budget_usd":1,"yes":true}`)}
	if _, err := runFindingsInvestigate(context.Background(), env, j, &testJobLog{}, nil); err != nil {
		t.Fatal(err)
	}
	sawFinal := false
	for _, s := range fake.Seen {
		for _, m := range s.Messages {
			if strings.Contains(m.Content, "sk-abcdefghijklmnopqrstuvwx123456") {
				t.Fatalf("the key in the final answer reached the prompt: %s", m.Content)
			}
			if strings.Contains(m.Content, "Final answer (step 0)") && strings.Contains(m.Content, "All done") {
				sawFinal = true
			}
		}
	}
	if !sawFinal {
		t.Fatal("the final answer was not in any prompt")
	}
}
