package handlers

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestEvaluationSkipReasonPlain checks that a planner refusal on the
// evaluation page reads as the plain planner sentence, without the internal
// "plan experiment: " prefix.
func TestEvaluationSkipReasonPlain(t *testing.T) {
	err := errors.New("plan e_x: suite lock: suite s@0.1.0 has no lock")
	got := planSkipReason(err)
	if got != PlainPlanError(err) {
		t.Fatalf("skip reason %q, want %q", got, PlainPlanError(err))
	}
	if strings.HasPrefix(got, "plan experiment: ") {
		t.Fatalf("skip reason keeps the internal prefix: %q", got)
	}
	each := plainEach([]string{"plan e_x: suite lock: suite s@0.1.0 has no lock", "plan e_y: something else"})
	if len(each) != 2 || each[0] != PlainPlanError(err) || each[1] != "something else" {
		t.Fatalf("plainEach = %q", each)
	}
}

// TestEstimateNoteHasNoForecastPrefix checks the note of a benchmark the
// planner refused during an estimate.
func TestEstimateNoteHasNoForecastPrefix(t *testing.T) {
	note := estimateRefusedNote(errors.New("plan e_estimate_1: suite lock: suite s@0.1.0 has no lock"))
	if !strings.HasPrefix(note, "Cannot estimate this benchmark: ") {
		t.Fatalf("note %q", note)
	}
	if strings.Contains(note, "no forecast: ") || strings.Contains(note, "plan e_estimate_1") {
		t.Fatalf("note keeps internal text: %q", note)
	}
}

// TestForecastRefusedNotesPlain checks the notes of a refused forecast.
func TestForecastRefusedNotesPlain(t *testing.T) {
	f := RefusedForecast(&schemas.Experiment{ID: "e_x"}, errors.New("plan e_x: suite lock: suite s@0.1.0 has no lock"), time.Unix(0, 0))
	if len(f.Notes) != 1 || strings.HasPrefix(f.Notes[0], "plan experiment: ") {
		t.Fatalf("notes %q", f.Notes)
	}
}

// TestPriceReasonOwnModel checks the reason given when a cost is not known.
func TestPriceReasonOwnModel(t *testing.T) {
	if got := priceReason([]string{""}); got != "the agent reports its own cost" {
		t.Fatalf("own model only: %q", got)
	}
	if got := priceReason([]string{"", "gpt-x"}); got != "no price for gpt-x" {
		t.Fatalf("mixed: %q", got)
	}
}
