package main

// Library priors for the first estimate (G4-02). An arm with no sealed
// history of its own is estimated from real runs of other agents on the
// same suite version: the org's own runs, plus the operator's library runs
// when HEIMDALL_LIBRARY_RUNS_ROOT names a run store (for example the runs
// root of the platform org that runs the reference agents on every library
// benchmark). Only per-run cost, tokens and wall time are read from it,
// summed into a range; no run, task output or org data of it is shown.

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/forecast"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// libraryRunsEnv names the operator's library run store.
const libraryRunsEnv = "HEIMDALL_LIBRARY_RUNS_ROOT"

var (
	libraryStoreOnce sync.Once
	libraryStore     forecast.Store
)

// operatorLibrary opens the operator's library run store once, or nil when
// none is configured or it does not open (logged).
func (a *app) operatorLibrary() forecast.Store {
	libraryStoreOnce.Do(func() {
		root := strings.TrimSpace(os.Getenv(libraryRunsEnv))
		if root == "" {
			return
		}
		st, err := store.NewFSStore(root, a.logger)
		if err != nil {
			a.logger.Warn("library runs for first estimates are off", "env", libraryRunsEnv, "error", err.Error())
			return
		}
		libraryStore = st
	})
	return libraryStore
}

// libraryRuns is every sealed run on m's suite version in the org's store
// and the operator's library store. A store that cannot be read gives no
// runs: the estimate then says it cannot estimate, as before.
func (a *app) libraryRuns(ctx context.Context, m *schemas.Manifest) []forecast.HistoryRun {
	var out []forecast.HistoryRun
	for _, st := range []forecast.Store{a.store, a.operatorLibrary()} {
		if st == nil {
			continue
		}
		h, err := forecast.LoadLibrary(ctx, st, m, sealedRunFile)
		if err != nil {
			a.logger.Warn("library runs not read", "experiment", m.ExpID, "error", err.Error())
			continue
		}
		out = append(out, h.Runs...)
	}
	return out
}

// reprice is the price ratio from model from to model to, per input and
// output token: low and high are the smaller and larger of the two ratios,
// mid the ratio of their sums. False when either model is unpriced.
func (a *app) reprice(from, to string) (float64, float64, float64, bool) {
	f, ok := a.prices.Lookup(from)
	if !ok {
		return 0, 0, 0, false
	}
	t, ok := a.prices.Lookup(to)
	if !ok {
		return 0, 0, 0, false
	}
	return priceRatio(f.InputPerMTok, f.OutputPerMTok, t.InputPerMTok, t.OutputPerMTok)
}

// priceRatio is reprice's arithmetic; a zero source price gives false.
func priceRatio(fromIn, fromOut, toIn, toOut float64) (float64, float64, float64, bool) {
	if fromIn <= 0 || fromOut <= 0 {
		return 0, 0, 0, false
	}
	rin, rout := toIn/fromIn, toOut/fromOut
	return min(rin, rout), (toIn + toOut) / (fromIn + fromOut), max(rin, rout), true
}
