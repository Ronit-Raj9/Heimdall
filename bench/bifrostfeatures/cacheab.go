package bifrostfeatures

import (
	"fmt"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// CacheRun is one sealed run of a "Bifrost cache on vs off" evaluation
// child: its gateway rows, whether it verified, and its cost.
type CacheRun struct {
	RunRows
	// Success is the run's verified success (nil when unknown).
	Success *bool
	// CostUSD is the run's model cost as Bifrost priced it (nil when
	// unknown); when nil the rows' bifrost_usd is summed instead.
	CostUSD *float64
}

// CacheArmStats is one arm's numbers. Means are over the runs or the model
// rows that carry the value; a count of zero means the value is unknown.
type CacheArmStats struct {
	Arm           string  `json:"arm"`
	Runs          int     `json:"runs"`
	Requests      int     `json:"requests"`        // model message rows
	CacheHits     int     `json:"cache_hits"`      // rows Bifrost served from the semantic cache
	HitRate       float64 `json:"hit_rate"`        // cache_hits over requests
	MeanLatencyMS float64 `json:"mean_latency_ms"` // per model row
	CostUSD       float64 `json:"cost_usd"`        // summed over runs
	CostRuns      int     `json:"cost_runs"`       // runs with a known cost
	Successes     int     `json:"successes"`
	SuccessRuns   int     `json:"success_runs"` // runs with a known outcome
	SuccessRate   float64 `json:"success_rate"`
}

// CacheABReport compares the cache-off and cache-on arms on the tasks both
// ran. Deltas are on minus off: a negative latency or cost delta is a saving,
// a negative success delta a quality drop.
type CacheABReport struct {
	Off            CacheArmStats `json:"off"`
	On             CacheArmStats `json:"on"`
	Tasks          int           `json:"tasks"` // tasks both arms ran
	LatencyDeltaMS float64       `json:"latency_delta_ms"`
	CostSavedUSD   float64       `json:"cost_saved_usd"` // off cost minus on cost, scaled to the same run count
	SuccessDelta   float64       `json:"success_delta"`
	Summary        string        `json:"summary"`
}

// CacheAB builds the report from a child's runs. Only the clean condition
// counts (faults change the requests, so they would hide the cache's
// effect), and only tasks both arms ran, so a skipped task on one side
// does not move the numbers.
func CacheAB(runs []CacheRun, offArm, onArm string) CacheABReport {
	tasks := map[string]map[string]bool{}
	for _, r := range runs {
		if r.Condition != "" && r.Condition != schemas.ConditionClean {
			continue
		}
		if tasks[r.Task] == nil {
			tasks[r.Task] = map[string]bool{}
		}
		tasks[r.Task][r.Arm] = true
	}
	both := map[string]bool{}
	for t, arms := range tasks {
		if arms[offArm] && arms[onArm] {
			both[t] = true
		}
	}
	rep := CacheABReport{Tasks: len(both)}
	rep.Off = armStats(runs, offArm, both)
	rep.On = armStats(runs, onArm, both)
	if rep.Off.Requests > 0 && rep.On.Requests > 0 {
		rep.LatencyDeltaMS = rep.On.MeanLatencyMS - rep.Off.MeanLatencyMS
	}
	if rep.Off.CostRuns > 0 && rep.On.CostRuns > 0 {
		perOff := rep.Off.CostUSD / float64(rep.Off.CostRuns)
		perOn := rep.On.CostUSD / float64(rep.On.CostRuns)
		rep.CostSavedUSD = (perOff - perOn) * float64(rep.On.CostRuns)
	}
	if rep.Off.SuccessRuns > 0 && rep.On.SuccessRuns > 0 {
		rep.SuccessDelta = rep.On.SuccessRate - rep.Off.SuccessRate
	}
	rep.Summary = cacheSummary(rep)
	return rep
}

func armStats(runs []CacheRun, arm string, tasks map[string]bool) CacheArmStats {
	st := CacheArmStats{Arm: arm}
	var latSum float64
	sorted := append([]CacheRun(nil), runs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].RunID < sorted[j].RunID })
	for _, r := range sorted {
		if r.Arm != arm || !tasks[r.Task] || (r.Condition != "" && r.Condition != schemas.ConditionClean) {
			continue
		}
		st.Runs++
		var rowCost float64
		rowCostKnown := false
		for _, row := range r.Rows {
			if row.Kind != "" && row.Kind != "message" {
				continue
			}
			st.Requests++
			latSum += row.LatencyMS
			if row.CacheHit {
				st.CacheHits++
			}
			if row.BifrostUSD != nil {
				rowCost += *row.BifrostUSD
				rowCostKnown = true
			}
		}
		switch {
		case r.CostUSD != nil:
			st.CostUSD += *r.CostUSD
			st.CostRuns++
		case rowCostKnown:
			st.CostUSD += rowCost
			st.CostRuns++
		}
		if r.Success != nil {
			st.SuccessRuns++
			if *r.Success {
				st.Successes++
			}
		}
	}
	if st.Requests > 0 {
		st.HitRate = float64(st.CacheHits) / float64(st.Requests)
		st.MeanLatencyMS = latSum / float64(st.Requests)
	}
	if st.SuccessRuns > 0 {
		st.SuccessRate = float64(st.Successes) / float64(st.SuccessRuns)
	}
	return st
}

func cacheSummary(rep CacheABReport) string {
	if rep.Tasks == 0 {
		return "No task ran with the cache both on and off yet."
	}
	if rep.On.CacheHits == 0 {
		return "The cache-on arm got no cache hits: the agent never repeated an identical request, so the cache saved nothing here."
	}
	s := "Bifrost cache served " + pct(rep.On.HitRate) + " of model requests"
	if rep.CostSavedUSD > 0 {
		s += ", saved $" + money(rep.CostSavedUSD)
	}
	if rep.Off.Requests > 0 && rep.LatencyDeltaMS < 0 {
		s += ", cut mean latency by " + ms(-rep.LatencyDeltaMS)
	}
	s += "."
	switch {
	case rep.Off.SuccessRuns == 0 || rep.On.SuccessRuns == 0:
	case rep.SuccessDelta < 0:
		s += " Success fell by " + pct(-rep.SuccessDelta) + " with the cache on: check for stale replies."
	case rep.SuccessDelta > 0:
		s += " Success rose by " + pct(rep.SuccessDelta) + " with the cache on."
	default:
		s += " Success was the same."
	}
	return s
}

func pct(f float64) string   { return fmt.Sprintf("%.0f%%", f*100) }
func money(f float64) string { return fmt.Sprintf("%.4f", f) }
func ms(f float64) string    { return fmt.Sprintf("%.0f ms", f) }

// CacheRunOf is r's CacheRun: the gateway rows given, success from the
// verified_success metric and cost from cost_usd, each left nil when the run
// did not observe it.
func CacheRunOf(r schemas.SealedRun, rows []schemas.GatewayRequest) CacheRun {
	cr := CacheRun{RunRows: RunRows{RunID: r.Run.RunID, Task: r.Slot.Task, Arm: r.Slot.Arm, Condition: r.Slot.Condition, Trial: r.Slot.Trial, Rows: rows}}
	if v, ok := r.Metrics["verified_success"]; ok && v.V != nil {
		b := *v.V >= 1
		cr.Success = &b
	}
	if v, ok := r.Metrics["cost_usd"]; ok && v.V != nil {
		c := *v.V
		cr.CostUSD = &c
	}
	return cr
}
