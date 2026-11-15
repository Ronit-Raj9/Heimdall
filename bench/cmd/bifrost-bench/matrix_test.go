package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// leaderboardMatrixQuery is the matrix command's default query.
func leaderboardMatrixQuery() leaderboard.MatrixQuery {
	return leaderboard.MatrixQuery{GradeSource: schemas.GradeSourceOriginal, Alpha: leaderboard.DefaultAlpha}
}

func TestMatrixUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"-exp"}, {"-exp", "e_x", "extra"}} {
		if code := runMatrix(args); code != exitUsage {
			t.Errorf("runMatrix(%v) = %d, want %d", args, code, exitUsage)
		}
	}
}

func TestMatrixCells(t *testing.T) {
	v := schemas.Observed(0.5, schemas.EvidenceOutcome)
	v.Bounds = &[2]float64{0.25, 0.75}
	if got := successText(v); got != "50.0% [25, 75]" {
		t.Errorf("successText = %q", got)
	}
	if got := successText(schemas.Observed(1, schemas.EvidenceOutcome)); got != "100.0%" {
		t.Errorf("successText without bounds = %q", got)
	}
	if got := successText(schemas.Unknown("x", schemas.EvidenceOutcome)); got != "unknown" {
		t.Errorf("successText unknown = %q", got)
	}
	if got := kText(schemas.Observed(0.25, schemas.EvidenceOutcome), 2); got != "25.0% (k=2)" {
		t.Errorf("kText = %q", got)
	}
	if got := valueText(schemas.NotApplicable(), moneyText); got != "n/a" {
		t.Errorf("valueText n/a = %q", got)
	}
	if got := valueText(schemas.Unknown("x", schemas.EvidenceGateway), moneyText); got != "unknown" {
		t.Errorf("valueText unknown = %q", got)
	}
	if got := valueText(schemas.Observed(0.0123, schemas.EvidenceGateway), moneyText); got != "$0.0123" {
		t.Errorf("valueText money = %q", got)
	}
}
