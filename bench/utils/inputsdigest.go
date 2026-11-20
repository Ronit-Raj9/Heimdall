package utils

import (
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// analysisInput is one sealed run as an analysis consumed it: its identity,
// its seal and the grade, gates and human review the grade source applied.
type analysisInput struct {
	RunID       string               `json:"run_id"`
	SlotID      string               `json:"slot_id"`
	SealHash    string               `json:"seal_hash"`
	GradeSource string               `json:"grade_source"`
	Grade       *schemas.Grade       `json:"grade,omitempty"`
	Gates       []schemas.GateResult `json:"gates,omitempty"`
	ReviewID    string               `json:"review_id,omitempty"`
	ReviewHash  string               `json:"review_hash,omitempty"`
	Problems    []string             `json:"problems,omitempty"`
}

// AnalysisInputsDigest is the sha256 of the ordered evidence an analysis was
// computed from: every sealed run SealedRuns returned, with its seal hash,
// grade source, grade, gates and effective review. An analysis records it
// and release creation recomputes it from the then-current store, so a
// review, regrade or run added after the analysis cannot be packaged with
// the earlier verdict (audit 1-A-03).
func AnalysisInputsDigest(runs []schemas.SealedRun) (string, error) {
	in := make([]analysisInput, 0, len(runs))
	for _, r := range runs {
		x := analysisInput{RunID: r.Run.RunID, SlotID: r.Slot.SlotID, SealHash: r.SealHash, GradeSource: r.GradeSource,
			Grade: r.Grade, Gates: r.Gates, Problems: r.Problems}
		if r.Review != nil {
			x.ReviewID, x.ReviewHash = r.Review.ReviewID, r.Review.Hash
		}
		in = append(in, x)
	}
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].SlotID != in[j].SlotID {
			return in[i].SlotID < in[j].SlotID
		}
		return in[i].RunID < in[j].RunID
	})
	return HashJSON(in)
}
