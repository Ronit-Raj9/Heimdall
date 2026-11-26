package improve

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/maximhq/bifrost/bench/stats"
)

// visibleFailures are the run outcomes the retries control retries (A2.6):
// failures a team can see and retry, never a plain wrong answer.
var visibleFailures = []string{"timeout", "budget", "crash", "refusal"}

func visibleFailure(x RunScore) bool {
	return x.Stop != "" || slices.Contains(visibleFailures, x.Outcome)
}

// wantControl reports whether the spec asks for control k. An old spec's
// budget_matched_v0 reads as retries_v0 (D8).
func wantControl(s *Spec, k string) bool {
	if slices.Contains(s.Controls, k) {
		return true
	}
	return k == ControlRetries && slices.Contains(s.Controls, ControlBudgetMatched)
}

// nullImprover runs the same number of rounds with the improver given no
// evidence and keeps every edit (A2.7). Its final version is arm C of the
// final experiment. After each round the cursor is saved, so a resumed run
// starts where it stopped (A1.7).
func (r *Runner) nullImprover(ctx context.Context, rec *Record) (*NullControl, error) {
	c := rec.Controls
	v, start, cost := rec.Base, 1, 0.0
	if cur := c.NullCursor; cur != nil {
		v, start, cost = cur.Version, cur.Round+1, cur.CostUSD
	}
	for n := start; n <= rec.Spec.Rounds; n++ {
		if err := r.hardStop(rec, "the comparison without feedback", rec.Spec.Improver.BudgetUSD); err != nil {
			return nil, err
		}
		res, err := r.Exec.Improve(ctx, ImproveRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: n, Incumbent: v,
			Evidence: nil, BudgetUSD: rec.Spec.Improver.BudgetUSD, WallS: rec.Spec.Improver.WallS, Null: true, Kind: KindNull,
			Model: rec.Spec.Improver.Model, Provider: rec.Spec.Improver.Provider, Seed: proposalSeed(&rec.Spec, n, 1), Limits: DefaultLimits})
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
		} else {
			cost += res.CostUSD
			c.CostUSD += res.CostUSD
			next, err := r.nullStep(ctx, rec, n, v, res)
			if err != nil {
				return nil, err
			}
			v = next
		}
		c.NullCursor = &NullCursor{Round: n, Version: v, CostUSD: cost}
		if err := r.saveLive(rec); err != nil {
			return nil, err
		}
	}
	return &NullControl{Final: v, CostUSD: cost, Score: ArmScore{PerTask: map[string]float64{}}}, nil
}

// nullStep freezes one null improver round's bundle; an unchanged or
// unusable bundle keeps v.
func (r *Runner) nullStep(ctx context.Context, rec *Record, n int, v Version, res *ImproveResult) (Version, error) {
	editable := Bundle{}
	for k, x := range res.Bundle {
		if rec.Spec.Editable.Allows(k) {
			editable[k] = x
		}
	}
	if editable.Hash() == v.BundleHash {
		return v, nil
	}
	doc, err := ApplyBundle(v.Doc, rec.Spec.Editable, editable)
	if err != nil {
		return v, nil
	}
	next, err := r.Exec.Freeze(ctx, v, doc, Lineage{ImprovementID: rec.ID, Round: n, BundleHash: editable.Hash(),
		ProducedByRun: res.RunID, Note: fmt.Sprintf("improvement %s null improver round %d", rec.ID, n), Editable: rec.Spec.Editable})
	if err != nil {
		return v, fmt.Errorf("freeze null improver round %d: %w", n, err)
	}
	if _, err := appendNode(r.dir(rec.ID), Node{Round: n, Ref: next.Ref, Version: next.Number, Parent: v.Ref, Author: "agent",
		BundleHash: next.BundleHash, ConfigHash: next.ConfigHash, Status: NodeControl, Reason: "null improver: no evidence, every edit kept",
		ProducedBy: res.RunID, At: r.now()}); err != nil {
		return v, err
	}
	return next, nil
}

// retries is the retries control (A2.6): version 0 retried on held-out
// tasks whose last attempt visibly failed, up to k attempts per task, where
// k is what the loop's own spend buys per held-out task (capped by
// retries.max_attempts), until no task is left or the retries spent the
// loop's spend. Compared paired by task with the final version.
func (r *Runner) retries(ctx context.Context, rec *Record, runs []RunScore) (*RetriesControl, error) {
	sealed := rec.Splits.Sealed
	loop := loopUSD(rec)
	c0, n0 := 0.0, 0
	last := map[string]RunScore{}
	for _, x := range runs {
		if x.Arm != 0 {
			continue
		}
		c0 += x.CostUSD
		n0++
		if x.Trial == 1 {
			last[x.Task] = x
		}
	}
	if n0 > 0 {
		c0 /= float64(n0)
	}
	k := rec.Spec.Rounds + 1
	if c0 > 0 && len(sealed) > 0 {
		k = int(math.Floor(loop / (c0 * float64(len(sealed)))))
	}
	maxK := rec.Spec.Retries.MaxAttempts
	if maxK < 1 {
		maxK = MaxRetryAttempts
	}
	k = max(1, min(maxK, k))
	rc := &RetriesControl{K: k, LoopUSD: loop}
	for j := 2; j <= k; j++ {
		tasks := []string{}
		for _, t := range sealed {
			if x, ok := last[t]; ok && visibleFailure(x) {
				tasks = append(tasks, t)
			}
		}
		slices.Sort(tasks)
		if len(tasks) == 0 || rc.UsedUSD >= loop {
			break
		}
		if err := r.hardStop(rec, "the retries check", float64(len(tasks))*c0); err != nil {
			return nil, err
		}
		res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: j, Phase: PhaseRetries,
			Arms: []Version{rec.Base}, Tasks: tasks, Trials: 1})
		if err != nil {
			return nil, fmt.Errorf("retries check attempt %d: %w", j, err)
		}
		rc.UsedUSD += res.CostUSD
		rec.Controls.CostUSD += res.CostUSD
		for _, x := range res.Runs {
			if x.Arm == 0 {
				last[x.Task] = x
				rc.Attempts++
			}
		}
	}
	b, _ := TaskMeans(runs, 1, sealed)
	rt := map[string]float64{}
	for t, x := range last {
		if x.Known {
			rt[t] = clamp01(x.Success)
		}
	}
	rc.Score = mean(mapValues(rt))
	e := TaskDiffs(rt, b, sealed)
	rc.Diff = mean(e)
	if lo, hi, ok := stats.PairedT(e, 0.80); ok {
		rc.CI80 = [2]float64{lo, hi}
	}
	return rc, nil
}

func mapValues(m map[string]float64) []float64 {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]float64, 0, len(m))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// loopUSD is the loop's own spend: improver, screen and audit.
func loopUSD(rec *Record) float64 {
	t := 0.0
	for _, rd := range rec.Rounds {
		t += rd.Cost.Improver + rd.Cost.Screen + rd.Cost.Audit
	}
	return t
}

// totalUSD is the whole run's spend: baseline, rounds, and the end-of-loop
// experiments (null improver, final, retries, extension), which are kept in
// Controls.CostUSD.
func totalUSD(rec *Record) float64 {
	t := 0.0
	if rec.Baseline != nil {
		t += rec.Baseline.CostUSD
	}
	for _, rd := range rec.Rounds {
		t += rd.Cost.Total()
	}
	if rec.Controls != nil {
		t += rec.Controls.CostUSD
	}
	return t
}
