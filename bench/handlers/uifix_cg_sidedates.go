package handlers

import "github.com/maximhq/bifrost/bench/schemas"

// fillSideDates sets each side's At from its evaluation's creation time
// (fix wave M7-78). Only evaluations of org (or with no org, local mode)
// are read, so a side never shows another organization's date.
func fillSideDates(sum *schemas.ReleaseSummary, evals []*schemas.Evaluation, org string) {
	if sum == nil {
		return
	}
	at := func(s *schemas.ReleaseSide) {
		if s == nil || s.EvaluationID == "" {
			return
		}
		for _, ev := range evals {
			if ev != nil && ev.ID == s.EvaluationID && (ev.Org == "" || ev.Org == org) && !ev.CreatedAt.IsZero() {
				t := ev.CreatedAt.UTC()
				s.At = &t
				return
			}
		}
	}
	at(&sum.Candidate)
	at(sum.Baseline)
}
