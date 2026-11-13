package handlers

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// PairsHandler serves matched runs: the arms of one (task, condition, trial)
// side by side, aligned, with the first observed divergence.
type PairsHandler struct {
	mode  schemas.Mode
	index ReadIndex
}

// NewPairsHandler creates the pairs handler. index may be nil.
func NewPairsHandler(mode schemas.Mode, index ReadIndex) *PairsHandler {
	return &PairsHandler{mode: mode, index: index}
}

// RegisterRoutes registers GET /pairs/{id}.
func (h *PairsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/pairs/{id}", lib.ChainMiddlewares(h.getPair, middlewares...))
}

// getPair handles GET /api/bench/pairs/{id}. Runs are each slot's counted
// run, counted arms first in arm order. The alignment is between the
// first two counted runs; with fewer it is empty. Event content stays on
// GET /runs/{id}/events; steps carry event ids only.
func (h *PairsHandler) getPair(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "pair")
	if !ok {
		return
	}
	counted := countedRuns(h.index.Pair(id))
	if len(counted) == 0 {
		sendNotFound(ctx, h.mode, "pair", id)
		return
	}
	views := make([]RunView, 0, len(counted))
	for _, rv := range counted {
		views = append(views, rv)
	}
	slices.SortFunc(views, func(x, y RunView) int {
		return cmp.Or(compareTrueFirst(x.Slot.Counted, y.Slot.Counted), cmp.Compare(x.Slot.Arm, y.Slot.Arm))
	})
	first := views[0].Slot
	out := schemas.Pair{
		PairID: id, ExpID: first.ExpID, Task: first.Task, Condition: first.Condition, Trial: first.Trial,
		Runs: make([]schemas.PairRun, 0, len(views)), Alignment: []schemas.AlignedStep{},
	}
	for _, rv := range views {
		metrics, err := readMetrics(h.index, rv.Run.RunID)
		if err != nil {
			sendDepError(ctx, h.mode, "read metrics of run "+rv.Run.RunID, err)
			return
		}
		out.Runs = append(out.Runs, schemas.PairRun{Arm: rv.Slot.Arm, Slot: rv.Slot, Run: rv.Run, Metrics: metrics})
	}
	if len(views) >= 2 && views[0].Slot.Counted && views[1].Slot.Counted {
		a, err := readEvents(h.index, views[0].Run.RunID)
		if err != nil {
			sendDepError(ctx, h.mode, "read events of run "+views[0].Run.RunID, err)
			return
		}
		b, err := readEvents(h.index, views[1].Run.RunID)
		if err != nil {
			sendDepError(ctx, h.mode, "read events of run "+views[1].Run.RunID, err)
			return
		}
		out.Alignment, out.FirstDivergence = AlignPair(a, b).Steps(views[0].Slot.Arm, views[1].Slot.Arm, a, b)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// readMetrics reads a run's metrics.json (metric name -> value). A run whose
// measure stage has not run yet has no metrics: an empty map, not zeros.
func readMetrics(index ReadIndex, runID string) (map[string]schemas.Value, error) {
	data, found, err := readRunFile(index, runID, FileMetrics)
	if err != nil {
		return nil, err
	}
	metrics := map[string]schemas.Value{}
	if !found {
		return metrics, nil
	}
	if err := json.Unmarshal(data, &metrics); err != nil {
		return nil, fmt.Errorf("parse %s of run %s: %w", FileMetrics, runID, err)
	}
	return metrics, nil
}
