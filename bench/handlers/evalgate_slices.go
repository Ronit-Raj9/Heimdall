package handlers

import (
	"strings"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// maxGateSlices caps the slice rules of one gate.
const maxGateSlices = 20

// normalizeGateSlices checks a gate's metadata slice rules (lane Y13) and
// drops rules with no check.
func normalizeGateSlices(g *schemas.EvaluationGate) error {
	if len(g.Slices) > maxGateSlices {
		return badf("gate: at most %d slice rules", maxGateSlices)
	}
	kept := g.Slices[:0]
	seen := map[string]bool{}
	for _, r := range g.Slices {
		r.Key, r.Value = strings.TrimSpace(r.Key), strings.TrimSpace(r.Value)
		if !reportcard.ValidSliceKey(r.Key) {
			return badf("gate: slice key %q: letters, digits, '_', '.' or '-'", r.Key)
		}
		if r.Value == "" || len(r.Value) > 200 || strings.ContainsAny(r.Value, "\n\r") {
			return badf("gate: slice %s needs a value of one line, at most 200 characters", r.Key)
		}
		if r.MinScore != nil && (*r.MinScore < 0 || *r.MinScore > 1) {
			return badf("gate: min_score for %s=%s must be between 0 and 1", r.Key, r.Value)
		}
		if r.MaxRegression < 0 || r.MaxRegression > 1 {
			return badf("gate: max_regression for %s=%s must be between 0 and 1 (0.05 is 5 points)", r.Key, r.Value)
		}
		if r.MinScore == nil && r.MaxRegression == 0 {
			continue
		}
		if seen[r.Key+"\x00"+r.Value] {
			return badf("gate: slice %s=%s is listed twice", r.Key, r.Value)
		}
		seen[r.Key+"\x00"+r.Value] = true
		kept = append(kept, r)
	}
	g.Slices = kept
	if len(g.Slices) == 0 {
		g.Slices = nil
	}
	return nil
}
