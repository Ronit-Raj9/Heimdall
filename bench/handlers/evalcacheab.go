package handlers

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/maximhq/bifrost/bench/bifrostfeatures"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Bifrost cache on vs off report of one evaluation (lane C08): the
// evaluation page reads it in "How this evaluation uses Bifrost". One report
// per benchmark child, built by bifrostfeatures.CacheAB from the sealed runs'
// Bifrost rows, verified success and cost.

// EvaluationCacheChild is one benchmark's cache on vs off report.
type EvaluationCacheChild struct {
	Benchmark    string                        `json:"benchmark"`
	ExperimentID string                        `json:"experiment_id"`
	Report       bifrostfeatures.CacheABReport `json:"report"`
}

// EvaluationCacheAB is GET /evaluations/{id}/bifrost/cache.
type EvaluationCacheAB struct {
	EvaluationID string `json:"evaluation_id"`
	// Enabled is false when the evaluation did not run the cache option;
	// Children is then empty.
	Enabled  bool                   `json:"enabled"`
	Children []EvaluationCacheChild `json:"children"`
}

func (h *bifrostUseHandler) cacheAB(ctx *fasthttp.RequestCtx) {
	ev := h.loadEvaluation(ctx)
	if ev == nil {
		return
	}
	var src leaderboard.Source
	if h.env.Services.Index != nil {
		src = LeaderboardSource(h.env.Services.Index)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, BuildEvaluationCacheAB(src, ev))
}

// BuildEvaluationCacheAB builds the report; src may be nil (no runs read).
func BuildEvaluationCacheAB(src leaderboard.Source, ev *schemas.Evaluation) EvaluationCacheAB {
	out := EvaluationCacheAB{EvaluationID: ev.ID, Enabled: ev.Spec.BifrostCache, Children: []EvaluationCacheChild{}}
	if !out.Enabled || src == nil {
		return out
	}
	offArm := "A"
	if len(ev.Spec.Arms) == 1 && ev.Spec.Arms[0].Label != "" {
		offArm = ev.Spec.Arms[0].Label
	}
	for _, c := range ev.Children {
		if c.ExperimentID == "" {
			continue
		}
		runs := cacheRunsOf(src, c.ExperimentID)
		if len(runs) == 0 {
			continue
		}
		// The cache-on arm by its label: B in new evaluations, "cache" in
		// older ones (withVariantArms).
		onArm := schemas.EvaluationCacheArm
		if m, ok := src.Manifest(c.ExperimentID); ok {
			if id := schemas.VariantArmID(m, schemas.EvaluationCacheSuffix); id != "" {
				onArm = id
			}
		}
		out.Children = append(out.Children, EvaluationCacheChild{Benchmark: c.Benchmark, ExperimentID: c.ExperimentID,
			Report: bifrostfeatures.CacheAB(runs, offArm, onArm)})
	}
	return out
}

// cacheRunsOf is the counted sealed runs of one experiment (the last sealed
// attempt per slot, requeued runs left out) with their Bifrost rows.
func cacheRunsOf(src leaderboard.Source, expID string) []bifrostfeatures.CacheRun {
	bySlot := map[string][]leaderboard.Run{}
	for _, r := range src.Runs(expID) {
		if r.Sealed && !r.Superseded && r.Run.Outcome != schemas.OutcomeAbortedInfra {
			bySlot[r.Slot.SlotID] = append(bySlot[r.Slot.SlotID], r)
		}
	}
	var out []bifrostfeatures.CacheRun
	for _, runs := range bySlot {
		r := slices.MaxFunc(runs, func(x, y leaderboard.Run) int { return cmp.Compare(x.Run.Attempt, y.Run.Attempt) })
		cr := bifrostfeatures.CacheRun{RunRows: bifrostfeatures.RunRows{RunID: r.Run.RunID, Task: r.Slot.Task, Arm: r.Slot.Arm, Condition: r.Slot.Condition, Trial: r.Slot.Trial}}
		if data, err := src.RunFile(r.Run.RunID, FileEvidence); err == nil {
			var ev struct {
				Requests *schemas.RequestLog `json:"requests"`
			}
			if json.Unmarshal(data, &ev) == nil && ev.Requests != nil {
				cr.Rows = ev.Requests.Requests
			}
		}
		if data, err := src.RunFile(r.Run.RunID, "metrics.json"); err == nil {
			var m map[string]schemas.Value
			if json.Unmarshal(data, &m) == nil {
				if v, ok := m[metrics.VerifiedSuccess]; ok && v.V != nil {
					b := *v.V >= 1
					cr.Success = &b
				}
				if v, ok := m[metrics.CostUSD]; ok && v.V != nil {
					c := *v.V
					cr.CostUSD = &c
				}
			}
		}
		out = append(out, cr)
	}
	slices.SortFunc(out, func(a, b bifrostfeatures.CacheRun) int { return cmp.Compare(a.RunID, b.RunID) })
	return out
}
