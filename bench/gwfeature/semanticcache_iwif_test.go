package gwfeature

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestCacheUnknownSimilaritySaidInPlainWords: hits whose similarity is not
// logged (exact repeats carry no hit type in the gateway row) are left out
// of the threshold counts, never counted as wrong, and the headline says
// so in plain words.
func TestCacheUnknownSimilaritySaidInPlainWords(t *testing.T) {
	runs := scMany(12, []float64{0.97}, nil)
	for i := 0; i < 3; i++ {
		task := fmt.Sprintf("x%d", i)
		runs = append(runs, scRun("a"+task, task, "A", true, 1, Row{}), scRun("b"+task, task, "B", false, 0, Row{CacheHit: true, CacheID: "k0"}))
	}
	e := computeCache(context.Background(), scInput(runs))
	d := scDetails(t, e)
	if d.UnknownSim != 3 {
		t.Fatalf("unknown similarity hits %d", d.UnknownSim)
	}
	for _, th := range d.Thresholds {
		if th.Wrong != 0 {
			t.Fatalf("threshold %.2f counted %d wrong", th.Threshold, th.Wrong)
		}
	}
	if !strings.Contains(e.Headline, "3 cache hits have no recorded similarity") || !strings.Contains(e.Headline, "not counted as wrong") {
		t.Fatalf("headline %q", e.Headline)
	}
	plain := computeCache(context.Background(), scInput(scMany(12, []float64{0.97}, nil)))
	if strings.Contains(plain.Headline, "no recorded similarity") {
		t.Fatalf("headline without unknown hits %q", plain.Headline)
	}
}

// TestCacheNoWrongAnswersIsNotCalledWrong: when no threshold qualifies only
// because the range of the wrong share is still wide, the headline does not
// say that a threshold served wrong answers.
func TestCacheNoWrongAnswersIsNotCalledWrong(t *testing.T) {
	e := computeCache(context.Background(), scInput(scMany(12, []float64{0.97}, nil)))
	if e.State != StateMeasured || strings.Contains(e.Headline, "served wrong answers") || !strings.Contains(e.Headline, "at most 2%") {
		t.Fatalf("effect state %s headline %q", e.State, e.Headline)
	}
}
