package release

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestComparisonSummary(t *testing.T) {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"A": {Config: schemas.AgentConfig{ID: "codex", Model: "m-old"}},
		"B": {Config: schemas.AgentConfig{ID: "codex", Model: "m-new"}},
	}}
	a := &schemas.Analysis{Naive: map[string]schemas.Ratio{"A": {Num: 3, Den: 10}, "B": {Num: 8, Den: 10}}}
	s := comparisonSummary(m, a, "B")
	if s == nil || s.Candidate.Label != "B" || s.Candidate.Score != 0.8 || s.Candidate.Runs != 10 || s.Candidate.Model != "m-new" {
		t.Fatalf("candidate %+v", s)
	}
	if s.Candidate.CILow <= 0.4 || s.Candidate.CILow >= 0.8 || s.Candidate.CIHigh <= 0.8 || s.Candidate.CIHigh > 1 {
		t.Fatalf("range %v %v", s.Candidate.CILow, s.Candidate.CIHigh)
	}
	if s.Baseline == nil || s.Baseline.Label != "A" || s.Baseline.Score != 0.3 {
		t.Fatalf("baseline %+v", s.Baseline)
	}
	if comparisonSummary(m, &schemas.Analysis{}, "B") != nil {
		t.Fatal("no counts must give no summary")
	}
}
