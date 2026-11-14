package leaderboard

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Exclusion reasons that are not an outcome.
const (
	ReasonNotSealed       = "not sealed"
	ReasonVerdictInvalid  = "experiment verdict invalid"
	reasonNoOutcome       = "no outcome"
	metricVerifiedSuccess = "verified_success"
)

// gradedRun is one graded run with the values its entry needs.
type gradedRun struct {
	rec     *record
	success float64 // 1 for a verified success, else 0
	metrics map[string]schemas.Value
}

// excludedRun is one run left off the board and why.
type excludedRun struct {
	rec    *record
	reason string
}

// pool holds graded runs by config hash, then condition.
type pool map[string]map[string][]gradedRun

// grade splits records into graded runs and excluded ones. A run is graded
// when it is sealed, its experiment's verdict is not invalid, and its
// outcome scores (schemas.Outcome.Score): intention to treat, so a timeout or
// a crash counts as a failure and never improves a rank. Success is the
// run's verified_success metric, the default primary endpoint, and the
// outcome's score when the metric is missing (a run measured before it
// existed). A record read from a regrade (readGradeSource) carries its
// outcome and metrics already; any other reads metrics.json.
func grade(ctx context.Context, src Source, recs []*record) (pool, []excludedRun, error) {
	p := pool{}
	var excluded []excludedRun
	for _, r := range recs {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("leaderboard: %w", err)
		}
		if reason := excludeReason(r); reason != "" {
			excluded = append(excluded, excludedRun{rec: r, reason: reason})
			continue
		}
		metrics := r.metrics
		if metrics == nil {
			var err error
			if metrics, err = readMetrics(src, r.run.RunID); err != nil {
				return nil, nil, err
			}
		}
		y, _ := r.run.Outcome.Score()
		if v, ok := metrics[metricVerifiedSuccess].Float(); ok {
			y = 0
			if v >= 0.5 {
				y = 1
			}
		}
		byCond, ok := p[r.hash]
		if !ok {
			byCond = map[string][]gradedRun{}
			p[r.hash] = byCond
		}
		byCond[r.slot.Condition] = append(byCond[r.slot.Condition], gradedRun{rec: r, success: y, metrics: metrics})
	}
	return p, excluded, nil
}

// excludeReason returns why a record stays off the board, or "" when it is
// graded. An invalid verdict wins over everything: the experiment's
// instrument failed, so none of its runs are evidence.
func excludeReason(r *record) string {
	switch {
	case r.exp.invalid:
		return ReasonVerdictInvalid
	case !r.sealed:
		return ReasonNotSealed
	}
	if _, ok := r.run.Outcome.Score(); ok {
		return ""
	}
	if r.run.Outcome == "" {
		return reasonNoOutcome
	}
	return string(r.run.Outcome)
}

// exclusions groups the excluded runs of one condition by reason, sorted by
// reason, each with its experiments newest first.
func exclusions(excluded []excludedRun, condition string) []schemas.LeaderboardExclusion {
	byReason := map[string][]*record{}
	for _, x := range excluded {
		if x.rec.slot.Condition == condition {
			byReason[x.reason] = append(byReason[x.reason], x.rec)
		}
	}
	out := make([]schemas.LeaderboardExclusion, 0, len(byReason))
	for reason, recs := range byReason {
		out = append(out, schemas.LeaderboardExclusion{Reason: reason, Runs: len(recs), Experiments: experimentsNewestFirst(recs)})
	}
	slices.SortFunc(out, func(x, y schemas.LeaderboardExclusion) int { return cmp.Compare(x.Reason, y.Reason) })
	return out
}

// experimentsNewestFirst returns the distinct experiments of recs, newest
// manifest first, ties by id.
func experimentsNewestFirst(recs []*record) []string {
	seen := map[string]*expInfo{}
	for _, r := range recs {
		seen[r.exp.id] = r.exp
	}
	exps := make([]*expInfo, 0, len(seen))
	for _, e := range seen {
		exps = append(exps, e)
	}
	slices.SortFunc(exps, newestFirst)
	out := make([]string, 0, len(exps))
	for _, e := range exps {
		out = append(out, e.id)
	}
	return out
}

// newestFirst orders experiments by manifest creation, newest first, ties by id.
func newestFirst(x, y *expInfo) int {
	return cmp.Or(y.m.CreatedAt.Compare(x.m.CreatedAt), cmp.Compare(x.id, y.id))
}

// readMetrics reads a run's metrics.json. A run without one has no values:
// an empty map, so every metric reads unknown, never zero.
func readMetrics(src Source, runID string) (map[string]schemas.Value, error) {
	data, found, err := readRunFile(src, runID, fileMetrics)
	metrics := map[string]schemas.Value{}
	if err != nil || !found {
		return metrics, err
	}
	if err := json.Unmarshal(data, &metrics); err != nil {
		return nil, fmt.Errorf("parse %s of run %s: %w", fileMetrics, runID, err)
	}
	return metrics, nil
}

// readRunFile reads one run file. A missing file returns found false and no
// error.
func readRunFile(src Source, runID, name string) ([]byte, bool, error) {
	data, err := src.RunFile(runID, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s of run %s: %w", name, runID, err)
	}
	return data, true, nil
}
