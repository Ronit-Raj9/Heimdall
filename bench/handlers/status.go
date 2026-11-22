package handlers

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// costMetric is the metrics.json entry experiment spend is summed from.
const costMetric = "cost_usd"

// StatusFacts is what an experiment's status needs beyond its manifest and
// runs: facts read from run files, the launcher and the analysis folder.
type StatusFacts struct {
	// Costs maps a sealed run's id to its cost_usd. A run with no entry has
	// no metrics.json.
	Costs      map[string]schemas.Value
	Active     bool   // the launcher is executing the experiment in this process
	AnalysisID string // newest analysis, empty when none exists
	// Cancelled: the experiment's newest run job was cancelled (Jobs area).
	Cancelled bool
	// NotRequired names the optional adaptive slots the frozen rule did not
	// release (verdict.NotRequiredSlots). A slot named here with no run is
	// left out of the progress: it is work the experiment will never do.
	NotRequired map[string]bool
}

// BuildStatus derives an experiment's progress from its manifest and runs.
// Every slot counts toward total, the compatibility runs included, because
// they are work the experiment still has to do. A slot is done when its
// counted run is sealed with an outcome the verdict can score, failed when
// its counted run's outcome is infra_error, aborted_infra, unknown or
// invalid_evaluator, and running while it has no counted run and an unsealed
// one. The three are disjoint.
//
// State: done once every slot has a counted run (analyzing while the
// launcher still holds it), running while the launcher is active or a run is
// unsealed, cancelled when its run job was cancelled with slots left, created
// before any run, and failed when the experiment stopped with slots left: a
// budget stop, a crash or a restart. It is pure.
func BuildStatus(m *schemas.Manifest, runs []RunView, f StatusFacts) schemas.ExperimentStatus {
	st := schemas.ExperimentStatus{
		ExpID: m.ExpID, Name: m.Experiment.Name, ManifestHash: m.Hash, FrozenAt: m.CreatedAt,
		SpentUSD: spentUSD(runs, f.Costs), AnalysisID: f.AnalysisID,
		Suite: cmp.Or(m.SuiteID, m.Experiment.Suite), SuiteVersion: m.SuiteVersion, Arms: slices.Sorted(maps.Keys(m.Arms)), // UI regroup (U2)
		EvaluationID: m.Experiment.EvaluationID,
	}
	counted := countedRuns(runs)
	unsealed := map[string]bool{}
	for _, rv := range runs {
		if !rv.Sealed {
			unsealed[rv.Slot.SlotID] = true
		}
		if t := rv.Run.Timing.Started; !t.IsZero() && (st.StartedAt == nil || t.Before(*st.StartedAt)) {
			st.StartedAt = &t
		}
	}
	var finished *time.Time
	for _, s := range m.Slots {
		rv, ok := counted[s.SlotID]
		if !ok && !unsealed[s.SlotID] && f.NotRequired[s.SlotID] {
			continue // adaptive: the frozen rule never released it (QC2-27-01)
		}
		st.Slots.Total++
		switch {
		case ok && failedOutcome(rv.Run.Outcome):
			st.Slots.Failed++
		case ok:
			st.Slots.Done++
		case unsealed[s.SlotID]:
			st.Slots.Running++
		}
		if t := rv.Run.Timing.Sealed; ok && t != nil && (finished == nil || t.After(*finished)) {
			finished = t
		}
	}
	switch complete := st.Slots.Total > 0 && st.Slots.Done+st.Slots.Failed == st.Slots.Total; {
	case complete && f.Active:
		st.State = schemas.ExperimentAnalyzing
	case complete:
		st.State, st.FinishedAt = schemas.ExperimentDone, finished
	case f.Active:
		st.State = schemas.ExperimentRunning
	case f.Cancelled: // Jobs area: stopped by a cancel, slots left; resume runs them
		st.State = schemas.ExperimentCancelled
	case len(unsealed) > 0:
		st.State = schemas.ExperimentRunning
	case len(runs) == 0:
		st.State = schemas.ExperimentCreatedState
	default:
		st.State = schemas.ExperimentFailed
	}
	return st
}

// countedRuns picks each slot's counted run the way the store does: the
// first sealed run whose outcome is not aborted_infra, else the last attempt
// when it is a sealed aborted_infra (its retries are spent or pending). A
// slot with only unsealed runs has none.
func countedRuns(runs []RunView) map[string]RunView {
	bySlot := map[string][]RunView{}
	for _, rv := range runs {
		bySlot[rv.Slot.SlotID] = append(bySlot[rv.Slot.SlotID], rv)
	}
	out := make(map[string]RunView, len(bySlot))
	for slotID, rs := range bySlot {
		slices.SortStableFunc(rs, func(x, y RunView) int { return x.Run.Attempt - y.Run.Attempt })
		i := slices.IndexFunc(rs, func(rv RunView) bool {
			return rv.Sealed && rv.Run.Outcome != schemas.OutcomeAbortedInfra && !rv.Superseded // run control: a requeued run never counts
		})
		if i >= 0 {
			out[slotID] = rs[i]
			continue
		}
		if last := rs[len(rs)-1]; last.Sealed && last.Run.Outcome == schemas.OutcomeAbortedInfra && !last.Superseded {
			out[slotID] = last
		}
	}
	return out
}

// failedOutcome reports whether a counted run's outcome gives the verdict no
// score: infra_error, aborted_infra, unknown, invalid_evaluator, or none.
func failedOutcome(o schemas.Outcome) bool {
	_, scored := o.Score()
	return !scored
}

// spentUSD sums cost_usd over every sealed run, retries and compatibility
// runs included, since all of them spent. A sealed aborted_infra run without
// metrics stopped before its agent started and made no model call. Any other
// run whose cost is unknown makes the sum unknown, bounded when every unknown
// carries bounds. Not applicable costs are left out.
func spentUSD(runs []RunView, costs map[string]schemas.Value) schemas.Value {
	var sum, lo, hi float64
	unknown, bounded, reason := false, true, ""
	for _, rv := range runs {
		if !rv.Sealed {
			continue
		}
		c, ok := costs[rv.Run.RunID]
		if !ok {
			if rv.Run.Outcome == schemas.OutcomeAbortedInfra {
				continue
			}
			c = schemas.Unknown("no_metrics", schemas.EvidenceGateway)
		}
		if c.State == schemas.ValueNotApplicable {
			continue
		}
		if v, observed := c.Float(); observed {
			sum += v
			continue
		}
		unknown = true
		if reason == "" {
			reason = c.Reason
		}
		if c.Bounds == nil {
			bounded = false
			continue
		}
		lo, hi = lo+c.Bounds[0], hi+c.Bounds[1]
	}
	switch {
	case !unknown:
		return schemas.Observed(sum, schemas.EvidenceGateway)
	case reason == "":
		reason = "cost_unknown"
	}
	if bounded {
		return schemas.UnknownWithin(reason, schemas.EvidenceGateway, sum+lo, sum+hi)
	}
	return schemas.Unknown(reason, schemas.EvidenceGateway)
}

// planConditions counts tasks, trials and slots per condition, in the order
// the experiment lists its conditions. Compatibility slots are left out: they
// are not part of the verdict's design.
func planConditions(m *schemas.Manifest) []schemas.PlanCondition {
	var order []string
	for _, c := range m.Experiment.Conditions {
		order = append(order, c.ID)
	}
	tasks := map[string]map[string]bool{}
	byCond := map[string]*schemas.PlanCondition{}
	for _, s := range m.Slots {
		if !s.Counted {
			continue
		}
		pc, ok := byCond[s.Condition]
		if !ok {
			pc = &schemas.PlanCondition{Condition: s.Condition}
			byCond[s.Condition] = pc
			tasks[s.Condition] = map[string]bool{}
			if !slices.Contains(order, s.Condition) {
				order = append(order, s.Condition)
			}
		}
		pc.Slots++
		pc.Trials = max(pc.Trials, s.Trial)
		tasks[s.Condition][s.Task] = true
	}
	out := make([]schemas.PlanCondition, 0, len(byCond))
	for _, id := range order {
		if pc, ok := byCond[id]; ok {
			pc.Tasks = len(tasks[id])
			out = append(out, *pc)
		}
	}
	return out
}
