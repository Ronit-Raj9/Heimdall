package improve

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/maximhq/bifrost/bench/stats"
)

// controls runs the end-of-loop control arms the spec asks for.
func (r *Runner) controls(ctx context.Context, rec *Record) error {
	if rec.Controls == nil {
		rec.Controls = &Controls{}
	}
	c := rec.Controls
	if slices.Contains(rec.Spec.Controls, ControlBudgetMatched) && c.BudgetMatched == nil {
		bm, err := r.budgetMatched(ctx, rec)
		if err != nil {
			return err
		}
		c.BudgetMatched = bm
		c.CostUSD += bm.CostUSD
		if err := r.save(rec); err != nil {
			return err
		}
	}
	if slices.Contains(rec.Spec.Controls, ControlNullImprover) && c.Null == nil {
		nc, err := r.nullImprover(ctx, rec)
		if err != nil {
			return err
		}
		c.Null = nc
		c.CostUSD += nc.CostUSD
	}
	return nil
}

// budgetMatched gives version 0 the loop's own spend as extra trials on
// sealed: the gain must beat what the same money buys without improving.
// With no known per-trial cost (a free scripted agent) it uses one trial
// per round plus one.
func (r *Runner) budgetMatched(ctx context.Context, rec *Record) (*BudgetMatched, error) {
	spent := loopUSD(rec)
	sweep := 0.0 // cost of one sealed trial of version 0 across the sealed tasks
	if b := rec.Baseline; b != nil && rec.Spec.Sealed.Trials > 0 {
		sweep = b.SealedUSD / float64(rec.Spec.Sealed.Trials)
	}
	k := rec.Spec.Rounds + 1
	if sweep > 0 {
		k = int(math.Floor(spent / sweep))
	}
	k = max(1, min(MaxTrials, k))
	res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Phase: PhaseControl,
		Arms: []Version{rec.Base}, Tasks: rec.Splits.Sealed, Trials: k})
	if err != nil {
		return nil, fmt.Errorf("comparison run with the same spend: %w", err)
	}
	s := score(res.Runs, 0)
	bm := &BudgetMatched{ExpID: res.ExpID, Trials: k, LoopUSD: spent, Score: s, PerTrialUSD: sweep, CostUSD: res.CostUSD}
	best := map[string]float64{}
	for _, x := range res.Runs {
		if x.Known {
			best[x.Task] = math.Max(best[x.Task], clamp01(x.Success))
		}
	}
	vals := []float64{}
	for _, v := range best {
		vals = append(vals, v)
	}
	if len(vals) > 0 {
		bm.BestOfK = stats.Mean(vals)
	}
	per := []float64{}
	for _, v := range s.PerTask {
		per = append(per, v)
	}
	bm.BandLow, bm.BandHigh = s.Mean, s.Mean
	if n := len(per); n > 1 {
		half := 1.96 * stats.SD(per) / math.Sqrt(float64(n))
		bm.BandLow, bm.BandHigh = math.Max(0, s.Mean-half), math.Min(1, s.Mean+half)
	}
	return bm, nil
}

// nullImprover runs the same number of rounds with the improver given no
// evidence, keeps every edit, and scores the last version on sealed: the
// gain must beat edits made without feedback.
func (r *Runner) nullImprover(ctx context.Context, rec *Record) (*NullControl, error) {
	v := rec.Base
	nc := &NullControl{}
	for n := 1; n <= rec.Spec.Rounds; n++ {
		res, err := r.Exec.Improve(ctx, ImproveRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: n, Incumbent: v,
			Evidence: nil, BudgetUSD: rec.Spec.Improver.BudgetUSD, WallS: rec.Spec.Improver.WallS, Null: true})
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		nc.CostUSD += res.CostUSD
		editable := Bundle{}
		for k, x := range res.Bundle {
			if rec.Spec.Editable.Allows(k) {
				editable[k] = x
			}
		}
		if editable.Hash() == v.BundleHash {
			continue
		}
		doc, err := ApplyBundle(v.Doc, rec.Spec.Editable, editable)
		if err != nil {
			continue
		}
		next, err := r.Exec.Freeze(ctx, v, doc, Lineage{ImprovementID: rec.ID, Round: n, BundleHash: editable.Hash(),
			ProducedByRun: res.RunID, Note: fmt.Sprintf("improvement %s null improver round %d", rec.ID, n)})
		if err != nil {
			return nil, fmt.Errorf("freeze null improver round %d: %w", n, err)
		}
		if _, err := appendNode(r.dir(rec.ID), Node{Round: n, Ref: next.Ref, Version: next.Number, Parent: v.Ref, Author: "agent",
			BundleHash: next.BundleHash, ConfigHash: next.ConfigHash, Status: NodeControl, Reason: "null improver: no evidence, every edit kept",
			ProducedBy: res.RunID, At: r.now()}); err != nil {
			return nil, err
		}
		v = next
	}
	res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Phase: PhaseControl,
		Arms: []Version{v}, Tasks: rec.Splits.Sealed, Trials: rec.Spec.Sealed.Trials})
	if err != nil {
		return nil, fmt.Errorf("comparison run without feedback: %w", err)
	}
	nc.ExpID, nc.Final, nc.Score = res.ExpID, v, score(res.Runs, 0)
	nc.CostUSD += res.CostUSD
	return nc, nil
}

// buildVerdict states what the run supports. "supported" needs a commit, a
// sealed gain over version 0 that the paired sign-flip test supports at the
// spec's level, a final sealed score above the budget-matched band and the
// null improver, and no overridden tripwire.
func buildVerdict(rec *Record) *Verdict {
	v := &Verdict{Notes: []string{}}
	for _, rd := range rec.Rounds {
		switch rd.Decision {
		case DecisionCommitted:
			v.Commits++
		case DecisionRejected:
			v.Rejects++
		}
		if len(rd.Tripwires) > 0 {
			v.Tripwires++
		}
		v.Overrides += len(rd.Approvals)
		if rd.Audit != nil && rd.Audit.Greedy && rd.Decision != DecisionCommitted {
			v.GreedyExtra++
		}
	}
	v.TotalUSD = totalUSD(rec)
	if rec.Baseline == nil {
		v.State, v.Headline = VerdictInconclusive, "no baseline was scored"
		return v
	}
	final := finalSealed(rec)
	d := taskDiffs(rec.Baseline.Sealed, final, rec.Splits.Sealed)
	if len(d) > 0 {
		v.SealedGain = stats.Mean(d)
		sf := stats.SignFlip(d)
		v.P = &sf.P
	}
	if dev := finalDev(rec); dev != nil {
		v.DevGain = dev.Mean - rec.Baseline.Dev.Mean
	}
	v.GenGap = v.DevGain - v.SealedGain
	if v.TotalUSD > 0 {
		g := v.SealedGain / v.TotalUSD
		v.GainPerUSD = &g
	}
	v.Notes = append(v.Notes, "a single loop: rerun the improvement from version 0 to show the gain holds across loops")
	if v.GreedyExtra > 0 {
		v.Notes = append(v.Notes, fmt.Sprintf("keeping every version that scored higher would have kept %d more the evidence test refused", v.GreedyExtra))
	}
	beatsControls, why := true, ""
	if c := rec.Controls; c != nil {
		if bm := c.BudgetMatched; bm != nil && final.Mean <= bm.BandHigh {
			beatsControls, why = false, fmt.Sprintf("the final held-out score %.0f%% is inside what version 0 reaches with the same spend (up to %.0f%%)", 100*final.Mean, 100*bm.BandHigh)
		}
		if nc := c.Null; nc != nil && final.Mean <= nc.Score.Mean {
			beatsControls, why = false, fmt.Sprintf("edits made without feedback scored %.0f%%, not below the final %.0f%%", 100*nc.Score.Mean, 100*final.Mean)
		}
	}
	gain := fmt.Sprintf("%+.1f points on the held-out tasks", 100*v.SealedGain)
	switch {
	case v.Commits == 0:
		v.State, v.Headline = VerdictNoChange, "no new version was kept; the agent stays at version 0"
	case v.SealedGain <= 0:
		v.State, v.Headline = VerdictNotSupported, "improvement not supported: "+gain
	case v.P != nil && *v.P < rec.Spec.Commit.Alpha && beatsControls && v.Overrides == 0:
		v.State, v.Headline = VerdictSupported, "improvement supported: "+gain+", above both comparisons"
	default:
		v.State, v.Headline = VerdictInconclusive, "inconclusive: "+gain
		switch {
		case v.Overrides > 0:
			v.Notes = append(v.Notes, "a paused check was approved by a person, so the gain cannot be claimed")
		case !beatsControls:
			v.Notes = append(v.Notes, why)
		default:
			v.Notes = append(v.Notes, "the held-out gain is not significant with this many tasks")
		}
	}
	return v
}

// finalSealed is the incumbent's last sealed score (the baseline when no
// round scored one).
func finalSealed(rec *Record) ArmScore {
	for i := len(rec.Rounds) - 1; i >= 0; i-- {
		if s := rec.Rounds[i].Sealed; s != nil {
			return *s
		}
	}
	return rec.Baseline.Sealed
}

// finalDev is the incumbent's last dev score from a screen.
func finalDev(rec *Record) *ArmScore {
	for i := len(rec.Rounds) - 1; i >= 0; i-- {
		rd := rec.Rounds[i]
		if rd.Screen == nil {
			continue
		}
		if rd.Decision == DecisionCommitted {
			return &rd.Screen.B
		}
		return &rd.Screen.A
	}
	return &rec.Baseline.Dev
}

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
