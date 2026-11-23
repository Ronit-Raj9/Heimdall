package reportcard

import (
	"slices"
	"testing"
	"time"

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

// TestSelectForAgentByEvaluationID: the agent card shows the evaluation the
// page asked for (?evaluation=) when it ran the agent, else the newest one
// that did; MatchingEvaluations lists the done ones newest first (N206).
func TestSelectForAgentByEvaluationID(t *testing.T) {
	now := time.Now()
	arm := func(label, agent, model string) schemas.EvaluationArm {
		return schemas.EvaluationArm{Label: label, Agent: agent, Model: model}
	}
	evals := []schemas.Evaluation{
		{ID: "ev_old", Status: StatusDone, CreatedAt: now.Add(-2 * time.Hour), Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("A", "codex", "m1")}}},
		{ID: "ev_new", Status: StatusDone, CreatedAt: now.Add(-time.Hour), Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("A", "codex", "m1"), arm("B", "codex", "m2")}}},
		{ID: "ev_other", Status: StatusDone, CreatedAt: now, Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("A", "other", "m1")}}},
		{ID: "ev_failed", Status: "failed", CreatedAt: now, Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{arm("A", "codex", "m1")}}},
	}
	cases := []struct {
		id, model, want string
		labels          int
	}{
		{"", "", "ev_new", 2},
		{"ev_old", "", "ev_old", 1},
		{"ev_old", "m2", "ev_new", 1},   // ev_old never ran m2: the newest matching one
		{"ev_other", "", "ev_new", 2},   // another agent's evaluation is not shown
		{"ev_failed", "", "ev_new", 2},  // a failed evaluation is not shown
		{"ev_missing", "", "ev_new", 2}, // an unknown id falls back
	}
	for _, c := range cases {
		ev, labels := SelectForAgentEvaluation(evals, "codex", 0, c.model, c.id)
		got := ""
		if ev != nil {
			got = ev.ID
		}
		if got != c.want || len(labels) != c.labels {
			t.Errorf("%q %q: got %q %v, want %q with %d labels", c.id, c.model, got, labels, c.want, c.labels)
		}
	}
	var ids []string
	for _, ev := range MatchingEvaluations(evals, "codex", 0, "") {
		ids = append(ids, ev.ID)
	}
	if want := []string{"ev_new", "ev_old"}; !slices.Equal(ids, want) {
		t.Fatalf("matching %v, want %v", ids, want)
	}
	if got := MatchingEvaluations(evals, "codex", 0, "m2"); len(got) != 1 || got[0].ID != "ev_new" {
		t.Fatalf("matching m2 %v", got)
	}
}
