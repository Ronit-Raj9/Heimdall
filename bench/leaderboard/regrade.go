package leaderboard

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RegradeSource is optionally implemented by a Source. It reads an
// experiment's regrades (regrades/<regrade_id>/ beside its sealed runs), so a
// board with grade source latest can read recomputed gates, outcomes and
// metrics. A Source without it has no regrades: every run reads as sealed.
type RegradeSource interface {
	// Regrades lists an experiment's regrades, in any order. An experiment
	// with none returns an empty list and no error.
	Regrades(ctx context.Context, expID string) ([]Regrade, error)
	// RegradedRuns returns the experiment's counted sealed runs read with the
	// regrade as grade source, as analysis reads them
	// (store.FSStore.SealedRuns): a run the regrade has a result for carries
	// the regrade's outcome, Y and metrics, with GradeSource the regrade id;
	// any other run is as sealed, with GradeSource "original".
	RegradedRuns(ctx context.Context, expID, regradeID string) ([]schemas.SealedRun, error)
}

// Regrade is one regrade of an experiment, from its regrade.json.
type Regrade struct {
	ID        string
	CreatedAt time.Time
}

// newestRegrade returns the regrade with the latest CreatedAt, ties broken by
// the larger id so the choice never depends on listing order, and false for
// an empty list.
func newestRegrade(rs []Regrade) (Regrade, bool) {
	if len(rs) == 0 {
		return Regrade{}, false
	}
	return slices.MaxFunc(rs, func(x, y Regrade) int {
		return cmp.Or(x.CreatedAt.Compare(y.CreatedAt), cmp.Compare(x.ID, y.ID))
	}), true
}

// readGradeSource applies the grade source to recs. Under original it does
// nothing: every run reads as sealed. Under latest, each experiment with a
// regrade is read with its newest one (newestRegrade): a sealed record whose
// run the regrade lists takes that run's outcome, Y and metrics, so a run a
// recomputed gate no longer vetoes is graded as the analysis would grade it.
// A record the regrade does not list (a compatibility run, which analysis
// never reads, or a run sealed after the regrade) keeps its original grade.
// A regrade that fails to read, for example a regate whose hash or parent
// seal does not verify, fails the board rather than falling back silently.
func readGradeSource(ctx context.Context, src Source, recs []*record, source string) error {
	if source == GradeSourceReviewed {
		return readReviews(ctx, src, recs) // human review (review.go)
	}
	if source != GradeSourceLatest {
		return nil
	}
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
		list, err := rs.Regrades(ctx, exp.id)
		if err != nil {
			return fmt.Errorf("list regrades of experiment %s: %w", exp.id, err)
		}
		newest, ok := newestRegrade(list)
		if !ok {
			continue
		}
		runs, err := rs.RegradedRuns(ctx, exp.id, newest.ID)
		if err != nil {
			return fmt.Errorf("read regrade %s of experiment %s: %w", newest.ID, exp.id, err)
		}
		exp.regrade = newest.ID
		byRun := make(map[string]*schemas.SealedRun, len(runs))
		for i := range runs {
			byRun[runs[i].Run.RunID] = &runs[i]
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
			if sr.GradeSource != "" && sr.GradeSource != GradeSourceOriginal {
				r.source = sr.GradeSource
			}
		}
	}
	return nil
}

// gradeSourceList returns, for every experiment with a record under the
// condition, the source its runs were read from and how many of them came
// from the regrade, newest experiment first.
func gradeSourceList(recs []*record, condition string) []schemas.LeaderboardGradeSource {
	byExp := map[*expInfo]*schemas.LeaderboardGradeSource{}
	for _, r := range recs {
		if r.slot.Condition != condition {
			continue
		}
		gs, ok := byExp[r.exp]
		if !ok {
			gs = &schemas.LeaderboardGradeSource{Experiment: r.exp.id, Source: cmp.Or(r.exp.regrade, GradeSourceOriginal)}
			byExp[r.exp] = gs
		}
		gs.Runs++
		if r.source != "" {
			gs.RegradedRuns++
		}
		if r.review != nil {
			gs.ReviewedRuns++ // human review (review.go)
		}
	}
	exps := make([]*expInfo, 0, len(byExp))
	for e := range byExp {
		exps = append(exps, e)
	}
	slices.SortFunc(exps, newestFirst)
	out := make([]schemas.LeaderboardGradeSource, 0, len(exps))
	for _, e := range exps {
		out = append(out, *byExp[e])
	}
	return out
}
