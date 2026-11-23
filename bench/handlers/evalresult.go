package handlers

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// evalResultBackfillPerList bounds how many finished evaluations without a
// stored result one list request fills in (oldest first), so a list read
// never builds more than a few report cards.
const evalResultBackfillPerList = 3

// evalResultRetryAfter is how long the list backfill skips an evaluation
// whose report card failed to build, so a few broken old evaluations do not
// hold every backfill slot and newer ones still get filled.
const evalResultRetryAfter = 10 * time.Minute

// evalResultBuild builds the report card a stored result is read from; tests
// replace it to make an evaluation fail.
var evalResultBuild = reportcard.Build

// evalResultFailures remembers, per project root and evaluation id, when the
// last attempt to fill a result failed. It lives in memory only: a restart
// tries every evaluation again.
var evalResultFailures = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

func evalResultKey(root, id string) string { return root + "\x00" + id }

func (h *evaluationsHandler) clock() time.Time {
	if h.now == nil {
		return time.Now()
	}
	return h.now()
}

// noteResultFailure records a failed fill at now, or clears the record when
// ok, and drops records older than evalResultRetryAfter.
func (h *evaluationsHandler) noteResultFailure(id string, now time.Time, ok bool) {
	f := &evalResultFailures
	f.Lock()
	defer f.Unlock()
	key := evalResultKey(h.root(), id)
	if ok {
		delete(f.at, key)
		return
	}
	for k, at := range f.at {
		if now.Sub(at) >= evalResultRetryAfter {
			delete(f.at, k)
		}
	}
	f.at[key] = now
}

// resultOnHold is true while a failed fill of this evaluation is younger
// than evalResultRetryAfter.
func (h *evaluationsHandler) resultOnHold(id string, now time.Time) bool {
	f := &evalResultFailures
	f.Lock()
	defer f.Unlock()
	at, ok := f.at[evalResultKey(h.root(), id)]
	return ok && now.Sub(at) < evalResultRetryAfter
}

// needsResult is true for a finished evaluation with no stored result, or
// with one stored before results recorded their analyses (refilled once,
// fix wave 3, N215).
func needsResult(ev *schemas.Evaluation) bool {
	if ev.Status != schemas.EvaluationDone {
		return false
	}
	return ev.Result == nil || (ev.Result.Analyses == nil && len(childExperiments(ev)) > 0)
}

// childExperiments are the experiment ids of the evaluation's children.
func childExperiments(ev *schemas.Evaluation) []string {
	var out []string
	for _, c := range ev.Children {
		if c.ExperimentID != "" {
			out = append(out, c.ExperimentID)
		}
	}
	return out
}

// resultAnalyses is each child experiment's newest analysis id, "" when it
// has none or the index cannot say.
func (h *evaluationsHandler) resultAnalyses(ev *schemas.Evaluation) map[string]string {
	ai, _ := h.env.Services.Index.(AnalysisIndex)
	out := map[string]string{}
	for _, id := range childExperiments(ev) {
		out[id] = ""
		if ai != nil {
			out[id], _ = ai.LatestAnalysis(id)
		}
	}
	return out
}

// resultStale is true when a child of a finished evaluation has a newer
// analysis than the one its stored result was built from (fix wave 3,
// N215): a re-score after a gate fix changes the score the list shows.
func (h *evaluationsHandler) resultStale(ev *schemas.Evaluation) bool {
	if ev.Status != schemas.EvaluationDone || ev.Result == nil || ev.Result.Analyses == nil {
		return false
	}
	if _, ok := h.env.Services.Index.(AnalysisIndex); !ok {
		return false
	}
	for id, a := range h.resultAnalyses(ev) {
		if ev.Result.Analyses[id] != a {
			return true
		}
	}
	return false
}

// fillResult stores the headline numbers of a finished evaluation once
// (fix wave 2, N161): each arm's clean score and runs from the report card
// totals and the clean spend summed over the arms. A child's newer analysis
// fills it again (fix wave 3, N215). It reports whether the
// evaluation changed; a failure is logged and remembered, so the list
// backfill skips it for evalResultRetryAfter (a single read still tries).
func (h *evaluationsHandler) fillResult(ev *schemas.Evaluation) bool {
	s := h.env.Services
	if s.Index == nil || (!needsResult(ev) && !h.resultStale(ev)) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	opt := (&reportCardHandler{env: h.env}).options(ctx)
	rep, err := evalResultBuild(ctx, LeaderboardSource(s.Index), ev, opt)
	now := h.clock()
	h.noteResultFailure(ev.ID, now, err == nil)
	if err != nil {
		slog.Warn("evaluation result not stored", "evaluation", ev.ID, "err", err)
		return false
	}
	res := &schemas.EvaluationResult{Arms: make([]schemas.EvaluationArmResult, 0, len(rep.Arms)), At: now.UTC(), Analyses: h.resultAnalyses(ev)}
	sum, known := 0.0, len(rep.Arms) > 0
	for _, a := range rep.Arms {
		t := rep.Totals[a.Label]
		res.Arms = append(res.Arms, schemas.EvaluationArmResult{Label: a.Label, Agent: a.Agent, Version: a.Version, Model: a.Model, Score: t.Score, Runs: t.Runs})
		if t.CostUSD == nil {
			known = false
		} else {
			sum += *t.CostUSD
		}
	}
	if known {
		res.CostUSD = &sum
	}
	ev.Result = res
	return true
}

// backfillResults fills the stored result of at most
// evalResultBackfillPerList finished evaluations of the list that have
// none, oldest first, and saves each one filled. An evaluation whose fill
// failed less than evalResultRetryAfter ago does not take a slot.
func (h *evaluationsHandler) backfillResults(evs []*schemas.Evaluation) {
	var todo []*schemas.Evaluation
	now := h.clock()
	for _, ev := range evs {
		if needsResult(ev) && !h.resultOnHold(ev.ID, now) {
			todo = append(todo, ev)
		}
	}
	slices.SortStableFunc(todo, func(a, b *schemas.Evaluation) int { return a.CreatedAt.Compare(b.CreatedAt) })
	for _, ev := range todo[:min(len(todo), evalResultBackfillPerList)] {
		if h.fillResult(ev) {
			_ = h.store().Save(ev) // a failed save only means it is filled again on a later read
		}
	}
}
