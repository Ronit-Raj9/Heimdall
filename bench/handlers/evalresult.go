package handlers

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// evalResultBackfillPerList bounds how many finished evaluations without a
// stored result one list request fills in (oldest first), so a list read
// never builds more than a few report cards.
const evalResultBackfillPerList = 3

// needsResult is true for a finished evaluation with no stored result.
func needsResult(ev *schemas.Evaluation) bool {
	return ev.Status == schemas.EvaluationDone && ev.Result == nil
}

// fillResult stores the headline numbers of a finished evaluation once
// (fix wave 2, N161): each arm's clean score and runs from the report card
// totals and the clean spend summed over the arms. It reports whether the
// evaluation changed; a failure is logged and tried again on a later read.
func (h *evaluationsHandler) fillResult(ev *schemas.Evaluation) bool {
	s := h.env.Services
	if !needsResult(ev) || s.Index == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	opt := (&reportCardHandler{env: h.env}).options(ctx)
	rep, err := reportcard.Build(ctx, LeaderboardSource(s.Index), ev, opt)
	if err != nil {
		slog.Warn("evaluation result not stored", "evaluation", ev.ID, "err", err)
		return false
	}
	now := h.now
	if now == nil {
		now = time.Now
	}
	res := &schemas.EvaluationResult{Arms: make([]schemas.EvaluationArmResult, 0, len(rep.Arms)), At: now().UTC()}
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
// none, oldest first, and saves each one filled.
func (h *evaluationsHandler) backfillResults(evs []*schemas.Evaluation) {
	var todo []*schemas.Evaluation
	for _, ev := range evs {
		if needsResult(ev) {
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
