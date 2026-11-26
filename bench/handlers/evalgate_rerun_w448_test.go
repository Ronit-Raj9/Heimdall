package handlers

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestReleaseBaselineSkipsRerunsOfFailures: the release gate's baseline is
// never a rerun of failures (it ran only the failed tasks).
func TestReleaseBaselineSkipsRerunsOfFailures(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	arms := []schemas.EvaluationArm{{Label: "A", Agent: "codex", Version: 2}}
	old := &schemas.Evaluation{ID: "ev_old", Status: schemas.EvaluationDone, CreatedAt: t0, Spec: schemas.EvaluationSpec{Arms: arms}}
	rerun := &schemas.Evaluation{ID: "ev_failures", Status: schemas.EvaluationDone, CreatedAt: t0.Add(time.Hour), RerunOf: "ev_old", Spec: schemas.EvaluationSpec{Arms: arms}}
	now := &schemas.Evaluation{ID: "ev_now", Status: schemas.EvaluationDone, CreatedAt: t0.Add(2 * time.Hour), Spec: schemas.EvaluationSpec{Arms: arms}}
	if prev, _ := previousRun([]*schemas.Evaluation{old, rerun, now}, now, "codex", 2); prev == nil || prev.ID != "ev_old" {
		t.Fatalf("release baseline = %+v", prev)
	}
}
