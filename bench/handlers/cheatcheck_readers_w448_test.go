package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestCheatCheckNeverABaseline: the release gate's baseline and the
// scheduled run's previous result never come from a cheat check.
func TestCheatCheckNeverABaseline(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	arms := []schemas.EvaluationArm{{Label: "A", Agent: "codex", Version: 2}}
	old := &schemas.Evaluation{ID: "ev_old", Status: schemas.EvaluationDone, CreatedAt: t0, Schedule: "nightly", Spec: schemas.EvaluationSpec{Arms: arms}}
	cheat := &schemas.Evaluation{ID: "ev_cheat", Status: schemas.EvaluationDone, CreatedAt: t0.Add(time.Hour), Schedule: "nightly", CheatCheckOf: "ev_old", Spec: schemas.EvaluationSpec{Arms: arms}}
	now := &schemas.Evaluation{ID: "ev_now", Status: schemas.EvaluationDone, CreatedAt: t0.Add(2 * time.Hour), Schedule: "nightly", Spec: schemas.EvaluationSpec{Arms: arms}}
	evals := []*schemas.Evaluation{old, cheat, now}
	if prev, _ := previousRun(evals, now, "codex", 2); prev == nil || prev.ID != "ev_old" {
		t.Fatalf("release baseline = %+v", prev)
	}
	if prev, _ := previousScheduledRun(evals, now, "A"); prev == nil || prev.ID != "ev_old" {
		t.Fatalf("scheduled previous = %+v", prev)
	}
}

// TestCheatCheckNotReleasable: a cheat check cannot be released.
func TestCheatCheckNotReleasable(t *testing.T) {
	h := &evalReleaseHandler{}
	ev := &schemas.Evaluation{ID: "ev_cheat", Status: schemas.EvaluationDone, CheatCheckOf: "ev_old",
		Children: []schemas.EvaluationChild{{Benchmark: "shop-twins", ExperimentID: "e1", Status: string(schemas.ExperimentDone)}}}
	p, r := h.buildPreview(context.Background(), ev)
	if r != nil || p.Problem != cheatCheckNotReleasable {
		t.Fatalf("preview problem %q resolution %v", p.Problem, r)
	}
}
