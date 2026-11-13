package handlers

import (
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// BuildStatus derives an experiment's progress from its manifest and runs.
// Every slot counts, the compatibility runs included, because they are work
// the experiment still has to do. A slot is done when its latest run is
// sealed with an outcome the verdict can score, failed when sealed without
// one (infra error, aborted, invalid evaluator, unknown), running while its
// run is unsealed. Spend is not aggregated here and stays unknown. It is pure.
func BuildStatus(m *schemas.Manifest, runs []RunView) schemas.ExperimentStatus {
	st := schemas.ExperimentStatus{
		ExpID: m.ExpID, Name: m.Experiment.Name, ManifestHash: m.Hash, FrozenAt: m.CreatedAt,
		SpentUSD: schemas.Unknown("not_aggregated", schemas.EvidenceGateway),
	}
	latest := latestRuns(runs)
	var started, finished *time.Time
	for _, s := range m.Slots {
		st.Slots.Total++
		rv, ok := latest[s.SlotID]
		if !ok {
			continue
		}
		if t := rv.Run.Timing.Started; !t.IsZero() && (started == nil || t.Before(*started)) {
			started = &t
		}
		switch _, scored := rv.Run.Outcome.Score(); {
		case !rv.Sealed:
			st.Slots.Running++
		case scored:
			st.Slots.Done++
		default:
			st.Slots.Failed++
		}
		if t := rv.Run.Timing.Sealed; t != nil && (finished == nil || t.After(*finished)) {
			finished = t
		}
	}
	switch {
	case st.Slots.Total > 0 && st.Slots.Done+st.Slots.Failed == st.Slots.Total:
		st.State = schemas.ExperimentDone
		st.FinishedAt = finished
	case started == nil && st.Slots.Running == 0:
		st.State = schemas.ExperimentCreatedState
	default:
		st.State = schemas.ExperimentRunning
	}
	st.StartedAt = started
	return st
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
