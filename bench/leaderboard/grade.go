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
	ReasonNotSealed      = "not sealed"
	ReasonVerdictInvalid = "experiment verdict invalid"
	ReasonSealMismatch   = "seal mismatch"
	ReasonDuplicateSlot  = "slot has more than one counted run"
	// ReasonEvidenceUnreadable: the run's seal verifies but a sealed file the
	// board reads (the proxy ledger) does not parse.
	ReasonEvidenceUnreadable = "run evidence unreadable"
	reasonNoOutcome          = "no outcome"
	metricVerifiedSuccess    = "verified_success"
)

// SealSource is optionally implemented by a Source that can check a sealed
// run's files against its seal.json and its experiment's seals.log.
// VerifySeal returns nil when the run verifies and an error wrapping
// schemas.ErrSealMismatch when it does not; the board then leaves the run out
// with reason "seal mismatch" rather than rank on files that are not the
// sealed evidence. Any other error fails the board. A Source without it is
// trusted as sealed.
type SealSource interface {
	VerifySeal(ctx context.Context, runID string) error
}

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
// when it is sealed, its experiment's verdict is not invalid, its slot has one
// counted run, its seal verifies (when src is a SealSource), and its
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
		reason := excludeReason(r)
		if reason == "" {
			var err error
			if reason, err = sealReason(ctx, src, r); err != nil {
				return nil, nil, err
			}
		}
		if reason != "" {
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
		y, _ := VerifiedSuccess(r.run.Outcome, metrics)
		byCond, ok := p[r.hash]
		if !ok {
			byCond = map[string][]gradedRun{}
			p[r.hash] = byCond
		}
		byCond[r.slot.Condition] = append(byCond[r.slot.Condition], gradedRun{rec: r, success: y, metrics: metrics})
	}
	return p, excluded, nil
}

// VerifiedSuccess is a run's verified success, 1 or 0, as every result
// surface reads it (board, matrix, trends, flat export): the observed
// verified_success metric when the run has one, else the outcome's score.
// ok is false when the outcome does not score; such a run has no success.
func VerifiedSuccess(o schemas.Outcome, metrics map[string]schemas.Value) (y float64, ok bool) {
	if y, ok = o.Score(); !ok {
		return 0, false
	}
	if v, has := metrics[metricVerifiedSuccess].Float(); has {
		y = 0
		if v >= 0.5 {
			y = 1
		}
	}
	return y, true
}

// Grading labels of a board (schemas.Leaderboard.Grading).
const (
	GradingVerified = schemas.GradingVerified
	GradingJudged   = schemas.GradingJudged
	GradingMixed    = "mixed"
	GradingUnknown  = "unknown"
)

// boardGrading reads how the board's graded runs on condition were graded:
// judged when the experiment's frozen plan pinned a judge or a run's sealed
// grade.json says a judge decided it, verified when a run's grade.json shows
// a deterministic grade, mixed for both, and unknown when no run carries
// grade evidence. The label travels with the board, so an archived result
// never depends on today's suite metadata. A grade.json that cannot be read
// adds nothing.
func boardGrading(ctx context.Context, src Source, p pool, condition string) (string, []string) {
	judged, verified := false, false
	judges := []string{}
	for _, byCond := range p {
		for _, g := range byCond[condition] {
			if ctx.Err() != nil {
				return GradingUnknown, nil
			}
			planJudged := g.rec.exp != nil && g.rec.exp.m != nil && g.rec.exp.m.Scenario["suite.judge"] != ""
			data, found, err := readRunFile(src, g.rec.run.RunID, fileGrade)
			var gr struct {
				Judged bool `json:"judged"`
				Judge  *struct {
					Model string `json:"model"`
				} `json:"judge"`
			}
			if err == nil && found && json.Unmarshal(data, &gr) == nil {
				if gr.Judged || planJudged {
					judged = true
					if gr.Judge != nil && gr.Judge.Model != "" && !slices.Contains(judges, gr.Judge.Model) {
						judges = append(judges, gr.Judge.Model)
					}
				} else {
					verified = true
				}
			} else if planJudged {
				judged = true
			}
		}
	}
	slices.Sort(judges)
	switch {
	case judged && verified:
		return GradingMixed, judges
	case judged:
		return GradingJudged, judges
	case verified:
		return GradingVerified, nil
	}
	return GradingUnknown, nil
}

// excludeReason returns why a record stays off the board, or "" when it is
// graded. An invalid verdict wins over everything: the experiment's
// instrument failed, so none of its runs are evidence.
func excludeReason(r *record) string {
	switch {
	case r.exp.invalid:
		return ReasonVerdictInvalid
	case externalReason(r) != "": // External APIs area: live or record runs are not repeatable (external.go)
		return ReasonExternalLive
	case simulatedUserReason(r) != "": // W3 customer support: a model-played user is another instrument (simuser.go)
		return ReasonSimulatedUser
	case !r.sealed:
		return ReasonNotSealed
	case r.counted > 1:
		return ReasonDuplicateSlot
	case r.sealMismatch:
		return ReasonSealMismatch
	case r.evidenceProblem != "":
		return ReasonEvidenceUnreadable
	}
	if _, ok := r.run.Outcome.Score(); ok {
		return ""
	}
	if r.run.Outcome == "" {
		return reasonNoOutcome
	}
	return string(r.run.Outcome)
}

// sealReason returns ReasonSealMismatch when src can verify seals and the
// record's sealed run does not verify, "" otherwise.
func sealReason(ctx context.Context, src Source, r *record) (string, error) {
	ss, ok := src.(SealSource)
	if !ok || !r.sealed || r.sealVerified {
		return "", nil
	}
	if r.sealMismatch {
		return ReasonSealMismatch, nil
	}
	err := ss.VerifySeal(ctx, r.run.RunID)
	switch {
	case err == nil:
		r.sealVerified = true
		return "", nil
	case errors.Is(err, schemas.ErrSealMismatch):
		r.sealMismatch = true
		return ReasonSealMismatch, nil
	default:
		return "", fmt.Errorf("leaderboard: verify seal of run %s: %w", r.run.RunID, err)
	}
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
