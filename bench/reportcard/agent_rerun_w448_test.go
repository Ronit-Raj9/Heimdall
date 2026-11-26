package reportcard

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestAgentCardSkipsRerunsOfFailures: a rerun of failures (only the failed
// tasks again) is never an agent's report card nor in its picker.
func TestAgentCardSkipsRerunsOfFailures(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	arms := []schemas.EvaluationArm{{Label: "A", Agent: "codex"}}
	evals := []schemas.Evaluation{
		{ID: "ev_parent", Status: StatusDone, CreatedAt: t0, Spec: schemas.EvaluationSpec{Arms: arms}},
		{ID: "ev_failures", Status: StatusDone, CreatedAt: t0.Add(time.Hour), RerunOf: "ev_parent", Spec: schemas.EvaluationSpec{Arms: arms}},
	}
	if ev, _ := SelectForAgent(evals, "codex", 0, ""); ev == nil || ev.ID != "ev_parent" {
		t.Fatalf("selected %+v", ev)
	}
	if ev, _ := SelectForAgentEvaluation(evals, "codex", 0, "", "ev_failures"); ev == nil || ev.ID != "ev_parent" {
		t.Fatalf("selected by id %+v", ev)
	}
	if got := MatchingEvaluations(evals, "codex", 0, ""); len(got) != 1 || got[0].ID != "ev_parent" {
		t.Fatalf("picker %+v", got)
	}
}
