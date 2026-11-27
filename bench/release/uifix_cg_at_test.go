package release

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestComparisonSummaryAt: both sides carry the comparison's date.
func TestComparisonSummaryAt(t *testing.T) {
	at := time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
	m := &schemas.Manifest{CreatedAt: at, Arms: map[string]schemas.ArmManifest{"A": {}, "B": {}}}
	a := &schemas.Analysis{Naive: map[string]schemas.Ratio{"A": {Num: 1, Den: 2}, "B": {Num: 2, Den: 2}}}
	s := comparisonSummary(m, a, "B")
	if s == nil || s.Candidate.At == nil || !s.Candidate.At.Equal(at) || s.Baseline == nil || s.Baseline.At == nil {
		t.Fatalf("%+v", s)
	}
}
