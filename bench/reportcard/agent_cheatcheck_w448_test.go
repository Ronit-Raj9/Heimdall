package reportcard

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestAgentCardSkipsCheatChecks: an agent's report card and its picker never
// pick a cheat check (impossible twins), which is not a score.
func TestAgentCardSkipsCheatChecks(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	arms := []schemas.EvaluationArm{{Label: "A", Agent: "codex"}}
	evals := []schemas.Evaluation{
		{ID: "ev_parent", Status: StatusDone, CreatedAt: t0, Spec: schemas.EvaluationSpec{Arms: arms}},
		{ID: "ev_cheat", Status: StatusDone, CreatedAt: t0.Add(time.Hour), CheatCheckOf: "ev_parent", Spec: schemas.EvaluationSpec{Arms: arms}},
	}
	if ev, _ := SelectForAgent(evals, "codex", 0, ""); ev == nil || ev.ID != "ev_parent" {
		t.Fatalf("selected %+v", ev)
	}
	if ev, _ := SelectForAgentEvaluation(evals, "codex", 0, "", "ev_cheat"); ev == nil || ev.ID != "ev_parent" {
		t.Fatalf("selected by id %+v", ev)
	}
	if got := MatchingEvaluations(evals, "codex", 0, ""); len(got) != 1 || got[0].ID != "ev_parent" {
		t.Fatalf("picker %+v", got)
	}
}
