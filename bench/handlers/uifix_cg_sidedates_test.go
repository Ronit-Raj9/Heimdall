package handlers

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestFillSideDates: each side gets its own evaluation's date; an
// evaluation of another organization gives none.
func TestFillSideDates(t *testing.T) {
	t1, t2 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	evals := []*schemas.Evaluation{{ID: "ev_new", Org: "org_a", CreatedAt: t2}, {ID: "ev_old", Org: "org_a", CreatedAt: t1}, {ID: "ev_b", Org: "org_b", CreatedAt: t1}}
	sum := &schemas.ReleaseSummary{Candidate: schemas.ReleaseSide{EvaluationID: "ev_new"}, Baseline: &schemas.ReleaseSide{EvaluationID: "ev_old"}}
	fillSideDates(sum, evals, "org_a")
	if sum.Candidate.At == nil || !sum.Candidate.At.Equal(t2) || sum.Baseline.At == nil || !sum.Baseline.At.Equal(t1) {
		t.Fatalf("%+v %+v", sum.Candidate.At, sum.Baseline.At)
	}
	other := &schemas.ReleaseSummary{Candidate: schemas.ReleaseSide{EvaluationID: "ev_b"}}
	fillSideDates(other, evals, "org_a")
	if other.Candidate.At != nil {
		t.Fatal("another organization's date shown")
	}
	fillSideDates(nil, evals, "org_a")
}
