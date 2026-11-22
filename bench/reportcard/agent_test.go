package reportcard

import (
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestModelsForAgent: the Model filter of an agent's report card lists only
// the models its done or running evaluations used (N34).
func TestModelsForAgent(t *testing.T) {
	arm := func(agent, model string, version int) schemas.EvaluationArm {
		return schemas.EvaluationArm{Label: model, Agent: agent, Model: model, Version: version}
	}
	evals := []schemas.Evaluation{
		{ID: "ev_1", Status: StatusDone, Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("codex", "gpt-6-luna", 1), arm("agents/codex.yaml@v2", "gpt-6.2-sol", 0)}}},
		{ID: "ev_2", Status: StatusRunning, Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("codex", "gpt-6-luna", 2), arm("codex", "", 2)}}},
		{ID: "ev_3", Status: StatusDone, Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("other", "claude-x", 1)}}},
		{ID: "ev_4", Status: "failed", Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("codex", "never-ran", 1)}}},
	}
	if got, want := ModelsForAgent(evals, "codex", 0), []string{"gpt-6-luna", "gpt-6.2-sol"}; !slices.Equal(got, want) {
		t.Fatalf("any version: %v, want %v", got, want)
	}
	if got, want := ModelsForAgent(evals, "codex", 1), []string{"gpt-6-luna"}; !slices.Equal(got, want) {
		t.Fatalf("v1: %v, want %v", got, want)
	}
	if got := ModelsForAgent(evals, "nobody", 0); len(got) != 0 {
		t.Fatalf("unknown agent: %v", got)
	}
}
