package handlers

import (
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// ExperimentArmStats is one setup's raw numbers on a comparison list row
// (GET /experiments?arm_stats=1): its counted runs (one sealed run per slot,
// the latest counted attempt), how many of them passed, and the summed cost
// of those runs. CostUSD is nil unless every counted run's cost is known
// (an unpriced run is never $0); CostRuns counts the runs whose cost is
// known. PassRate is Passed over Runs, nil with no counted run. These are
// read from the run index and each run's metrics.json, the files the list
// already reads for spend; they are not the verdict, which the comparison
// page computes with its own rules.
type ExperimentArmStats struct {
	Arm      string   `json:"arm"`
	Runs     int      `json:"runs"`
	Passed   int      `json:"passed"`
	PassRate *float64 `json:"pass_rate"`
	CostUSD  *float64 `json:"cost_usd"`
	CostRuns int      `json:"cost_runs"`
}

// ExperimentListRow is a row of the paged experiments list: the status of
// GET /experiments plus, with arm_stats=1, each setup's numbers.
type ExperimentListRow struct {
	schemas.ExperimentStatus
	ArmStats []ExperimentArmStats `json:"arm_stats,omitempty"`
}

// ExperimentPagedList is GET /experiments with limit, offset, q or
// arm_stats: one page of rows in the list's usual order, with the paging.
type ExperimentPagedList struct {
	Experiments []ExperimentListRow `json:"experiments"`
	ListPage
}

// listExperimentsPaged answers GET /experiments?limit=&offset=&q=&arm_stats=
// and returns true, or returns false when none of them was given (the plain
// list answers). q matches the experiment id and name, ignoring case. Only
// the rows of the page are read for status, so a page costs its rows, not
// the whole list.
func (h *ExperimentsHandler) listExperimentsPaged(ctx *fasthttp.RequestCtx) bool {
	args := ctx.QueryArgs()
	p, ok := readListPaging(ctx, h.mode)
	if !ok {
		return true
	}
	stats := false
	switch strings.TrimSpace(string(args.Peek("arm_stats"))) {
	case "":
	case "1", "true":
		stats = true
	case "0", "false":
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "arm_stats must be 1 or true")
		return true
	}
	if !p.on && !args.Has("arm_stats") {
		return false
	}
	marks, ok := h.archiveMarks(ctx) // UI fix wave D1: archived= (uifix_d1_archive.go)
	if !ok {
		return true
	}
	if !p.on {
		p.limit = max(len(h.index.Experiments()), 1) // arm_stats alone: the whole list
	}
	var kept []*schemas.Manifest
	for _, id := range h.index.Experiments() {
		m, found := h.index.Manifest(id)
		if !found {
			continue
		}
		if p.match(m.ExpID, m.Experiment.Name) && marks.keep(m.ExpID) {
			kept = append(kept, m)
		}
	}
	lo, hi := p.window(len(kept))
	out := ExperimentPagedList{Experiments: make([]ExperimentListRow, 0, hi-lo), ListPage: p.page(len(kept))}
	for _, m := range kept[lo:hi] {
		runs := h.index.Runs(m.ExpID)
		row := ExperimentListRow{ExperimentStatus: h.listRow(m, runs)}
		row.Archived = marks.mark(m.ExpID)
		if stats {
			row.ArmStats = armStats(h.index, m, runs)
		}
		out.Experiments = append(out.Experiments, row)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
	return true
}

// armStats counts each manifest arm's counted runs, passes and cost, arms
// sorted by id. A run's cost counts when its metrics.json holds an observed
// cost_usd.
func armStats(index ReadIndex, m *schemas.Manifest, runs []RunView) []ExperimentArmStats {
	counted := countedRuns(runs)
	list := make([]RunView, 0, len(counted))
	for _, rv := range counted {
		list = append(list, rv)
	}
	metrics := runMetrics(index, list)
	byArm := map[string]*ExperimentArmStats{}
	costSum := map[string]float64{}
	arms := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		arms = append(arms, id)
	}
	for _, rv := range list {
		if !slices.Contains(arms, rv.Slot.Arm) {
			arms = append(arms, rv.Slot.Arm)
		}
	}
	slices.Sort(arms)
	for _, a := range arms {
		byArm[a] = &ExperimentArmStats{Arm: a}
	}
	for _, rv := range list {
		s := byArm[rv.Slot.Arm]
		s.Runs++
		if rv.Run.Outcome == schemas.OutcomePass {
			s.Passed++
		}
		if c, ok := metrics[rv.Run.RunID].values[costMetric]; ok && c.State == schemas.ValueObserved && c.V != nil {
			s.CostRuns++
			costSum[rv.Slot.Arm] += *c.V
		}
	}
	out := make([]ExperimentArmStats, 0, len(arms))
	for _, a := range arms {
		s := byArm[a]
		if s.Runs > 0 {
			r := float64(s.Passed) / float64(s.Runs)
			s.PassRate = &r
			if s.CostRuns == s.Runs {
				c := costSum[a]
				s.CostUSD = &c
			}
		}
		out = append(out, *s)
	}
	return out
}
