package reportcard

import (
	"cmp"
	"context"
	"slices"

	"github.com/maximhq/bifrost/bench/failover"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/schemas"
)

// failoverSection is the "Provider failover" section (lane C07) of an
// evaluation run with the failover option: the plain arm on clean runs is
// the clean arm, the fallback arm (failover.ArmID) under the primary outage
// is the failover arm, and the plain arm under the outage is the
// no-failover arm. nil when the option was off or no run counted.
func failoverSection(ctx context.Context, src leaderboard.Source, ev *schemas.Evaluation) (*failover.Report, error) {
	if ev.Spec.Failover == nil || !ev.Spec.Failover.Enabled {
		return nil, nil
	}
	var clean, with, without failover.RunCell
	for _, c := range ev.Children {
		if c.ExperimentID == "" {
			continue
		}
		if d, ok := src.LatestDecision(c.ExperimentID); ok && d == schemas.DecisionInvalid {
			continue
		}
		bySlot := map[string][]leaderboard.Run{}
		for _, r := range src.Runs(c.ExperimentID) {
			bySlot[r.Slot.SlotID] = append(bySlot[r.Slot.SlotID], r)
		}
		for _, runs := range bySlot {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			slices.SortStableFunc(runs, func(x, y leaderboard.Run) int { return cmp.Compare(x.Run.Attempt, y.Run.Attempt) })
			i := slices.IndexFunc(runs, func(r leaderboard.Run) bool {
				return r.Sealed && r.Run.Outcome != schemas.OutcomeAbortedInfra && !r.Superseded
			})
			if i < 0 || runs[i].Slot.Arm == "compat" {
				continue
			}
			r := runs[i]
			if _, scores := r.Run.Outcome.Score(); !scores {
				continue
			}
			cond := cmp.Or(r.Slot.Condition, schemas.ConditionClean)
			fb := r.Slot.Arm == failover.ArmID
			var cell *failover.RunCell
			switch {
			case !fb && cond == schemas.ConditionClean:
				cell = &clean
			case fb && cond == schemas.GenericProbePrimaryOutage:
				cell = &with
			case !fb && cond == schemas.GenericProbePrimaryOutage:
				cell = &without
			default:
				continue
			}
			m, err := readMetrics(src, r.Run.RunID)
			if err != nil {
				return nil, err
			}
			y, _ := leaderboard.VerifiedSuccess(r.Run.Outcome, m)
			cell.Runs++
			cell.Successes += int(y)
			if v, ok := num(m, metrics.FallbackUsed); ok && v > 0 {
				cell.FallbackRuns++
			}
			if v, ok := num(m, metrics.WallTimeS); ok {
				cell.WallS, cell.WallN = cell.WallS+v, cell.WallN+1
			}
			if v, ok := num(m, metrics.CostUSD); ok {
				cell.CostUSD, cell.CostN = cell.CostUSD+v, cell.CostN+1
			}
		}
	}
	if clean.Runs+with.Runs+without.Runs == 0 {
		return nil, nil
	}
	rep := failover.BuildRunReport(clean, with, without)
	return &rep, nil
}

func num(m map[string]schemas.Value, k string) (float64, bool) {
	v, ok := m[k]
	if !ok || v.V == nil {
		return 0, false
	}
	return *v.V, true
}
