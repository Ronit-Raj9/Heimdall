package leaderboard

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// GradeSourceReviewed reads every run as sealed, then applies human reviews
// (docs/human-review.md): a run with an effective review takes the review's
// outcome, Y and the metrics it decides, read as analysis reads it with
// `check -grade-source reviewed`. Regrades are not read under it: latest
// means the newest regrade and never applies reviews, and reviewed never
// applies a regrade, so neither board moves when the other kind of record is
// added.
const GradeSourceReviewed = schemas.GradeSourceReviewed

// readReviews applies grade source reviewed to recs: for every experiment,
// its counted sealed runs are read through the RegradeSource with grade
// source reviewed, and a sealed record whose run came back with a review
// takes that run's outcome, Y and metrics. An experiment with at least one
// reviewed run on the board lists source reviewed; the others stay original.
// A Source without RegradeSource has no reviews.
func readReviews(ctx context.Context, src Source, recs []*record) error {
	rs, ok := src.(RegradeSource)
	if !ok {
		return nil
	}
	byExp := map[*expInfo][]*record{}
	for _, r := range recs {
		byExp[r.exp] = append(byExp[r.exp], r)
	}
	exps := make([]*expInfo, 0, len(byExp))
	for e := range byExp {
		exps = append(exps, e)
	}
	slices.SortFunc(exps, func(x, y *expInfo) int { return cmp.Compare(x.id, y.id) })
	for _, exp := range exps {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("leaderboard: %w", err)
		}
		runs, err := rs.RegradedRuns(ctx, exp.id, GradeSourceReviewed)
		if err != nil {
			return fmt.Errorf("read reviews of experiment %s: %w", exp.id, err)
		}
		byRun := make(map[string]*schemas.SealedRun, len(runs))
		for i := range runs {
			if runs[i].Review != nil {
				byRun[runs[i].Run.RunID] = &runs[i]
			}
		}
		for _, r := range byExp[exp] {
			sr, ok := byRun[r.run.RunID]
			if !ok || !r.sealed {
				continue
			}
			r.run.Outcome, r.run.Y = sr.Run.Outcome, sr.Run.Y
			r.metrics = sr.Metrics
			if r.metrics == nil {
				r.metrics = map[string]schemas.Value{}
			}
			r.review = sr.Review
			exp.regrade = GradeSourceReviewed
		}
	}
	return nil
}

// reviewCounts returns how many records under condition took their outcome
// from a human review, and how many of those differ from the sealed outcome.
func reviewCounts(recs []*record, condition string) (reviewed, changed int) {
	for _, r := range recs {
		if r.review == nil || r.slot.Condition != condition {
			continue
		}
		reviewed++
		if r.review.SealedOutcome != r.run.Outcome {
			changed++
		}
	}
	return reviewed, changed
}

// writeReviewedSources writes the grade source footnote under grade source
// reviewed: what was read, that a reviewed outcome is a person's judgement,
// and how many runs of each experiment a review decided.
func writeReviewedSources(b *strings.Builder, lb *schemas.Leaderboard) {
	fmt.Fprintf(b, "Grade source reviewed: every run as sealed, with human reviews applied. %d run%s on this board took the outcome "+
		"from a human review (%d changed from the sealed outcome); a reviewed outcome is a person's judgement, evidence level reviewed, "+
		"not a verifier result. Regrades are not read under this source.\n\n", lb.ReviewedRuns, plural(lb.ReviewedRuns), lb.ReviewChangedRuns)
	if len(lb.GradeSources) == 0 {
		b.WriteString("- no experiment\n\n")
		return
	}
	for _, gs := range lb.GradeSources {
		fmt.Fprintf(b, "- %s: %d of %d run%s reviewed\n", gs.Experiment, gs.ReviewedRuns, gs.Runs, plural(gs.Runs))
	}
	b.WriteString("\n")
}
