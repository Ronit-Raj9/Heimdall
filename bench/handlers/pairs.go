package handlers

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// PairsHandler serves matched runs: the arms of one (task, condition, trial)
// side by side, aligned, with the first observed divergence.
type PairsHandler struct {
	mode           schemas.Mode
	index          ReadIndex
	heldOutVisible bool // server config held_out.visible (WithHeldOutVisible)
}

// WithHeldOutVisible sets whether a pair of a held-out task is served in
// full (server config held_out.visible). The default, false, serves only the
// documented metadata of such a pair: slot and run ids and outcomes, with no
// metrics and no alignment, and never reads its metrics or events (audit
// qc2-05-07).
func (h *PairsHandler) WithHeldOutVisible(visible bool) *PairsHandler {
	h.heldOutVisible = visible
	return h
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
	if !h.heldOutVisible && heldOutRun(h.index, &views[0]) {
		for _, rv := range views {
			r := rv.Run
			meta := schemas.Run{RunID: r.RunID, SlotID: r.SlotID, PairID: r.PairID, ExpID: r.ExpID, Attempt: r.Attempt, Outcome: r.Outcome}
			out.Runs = append(out.Runs, schemas.PairRun{Arm: rv.Slot.Arm, Slot: rv.Slot, Run: meta, Metrics: map[string]schemas.Value{}})
		}
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
		return
	}
	for _, rv := range views {
		metrics, err := readMetrics(h.index, rv.Run.RunID)
		if err != nil {
			sendDepError(ctx, h.mode, "read metrics of run "+rv.Run.RunID, err)
			return
		}
		out.Runs = append(out.Runs, schemas.PairRun{Arm: rv.Slot.Arm, Slot: rv.Slot, Run: rv.Run, Metrics: metrics})
	}
	if err := h.provenance(ctx, &out); err != nil {
		sendDepError(ctx, h.mode, "classify pair "+id, err)
		return
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

// provenance fills the pair's frozen suite reference and each run's own
// provenance. An arm whose config is scripted (or a smoke experiment) is
// scripted by the board's rules (leaderboard.SyntheticArms, without the
// ledger); any other run is classified from its own proxy ledger
// (leaderboard.LedgerProvenance). A ledger that cannot be read or parsed
// makes only that run's provenance unavailable, with a note: the rest of the
// pair stays readable (QC2-27-05). A pair whose manifest is not indexed
// keeps the defaults.
func (h *PairsHandler) provenance(ctx context.Context, out *schemas.Pair) error {
	m, ok := h.index.Manifest(out.ExpID)
	if !ok || m == nil {
		for i := range out.Runs {
			out.Runs[i].Provenance = leaderboard.ProvenanceUnavailable
			out.Runs[i].ProvenanceNote = "experiment manifest not indexed"
		}
		return nil
	}
	out.Suite, out.SuiteVersion = m.SuiteID, m.SuiteVersion
	configured, err := leaderboard.SyntheticArms(ctx, m, nil, nil)
	if err != nil {
		return err
	}
	for i := range out.Runs {
		pr := &out.Runs[i]
		if configured[pr.Arm] {
			pr.Synthetic, pr.Provenance = true, leaderboard.ProvenanceScripted
			continue
		}
		// The ledger is read only to classify; it is never served (runFileTypes).
		data, err := h.index.RunFile(pr.Run.RunID, proxyLedgerFile)
		switch {
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound):
			pr.Provenance = leaderboard.ProvenanceUnobserved
			continue
		case err != nil:
			pr.Provenance, pr.ProvenanceNote = leaderboard.ProvenanceUnavailable, "proxy ledger unreadable: "+err.Error()
			continue
		}
		prov, err := leaderboard.LedgerProvenance(data)
		if err != nil {
			pr.Provenance, pr.ProvenanceNote = leaderboard.ProvenanceUnavailable, "proxy ledger invalid: "+err.Error()
			continue
		}
		pr.Provenance = prov
		pr.Synthetic = prov == leaderboard.ProvenanceScripted || prov == leaderboard.ProvenanceMixed
	}
	return nil
}

// proxyLedgerFile is the run's proxy ledger, read only to classify.
const proxyLedgerFile = "proxy.ledger.jsonl"

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
