package handlers

import (
	"cmp"
	"context"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// GateSummaries aggregates every gate over the counted runs of counted slots,
// from their gates.json. Fired is runs fired over runs checked; a run sealed
// without that gate's result counts as unknown. Baseline comes from the
// catalog when one is given. Gates are sorted by name.
func GateSummaries(index ReadIndex, runs []RunView, catalog *schemas.Catalog) ([]schemas.GateSummary, error) {
	return gateSummaries(runs, catalog, func(runID string) ([]schemas.GateResult, error) {
		var results []schemas.GateResult
		_, err := readJSONFile(index, runID, FileGates, &results)
		return results, err
	})
}

// GateSummariesFor is GateSummaries read with a grade source. Under a
// regrade id each run's gates are the ones that regrade recomputed (a regate
// re-checks gates whose version changed and carries the rest over), so the
// checks list under a re-scored verdict shows the re-score's gate versions
// and fired counts, not the sealed ones. Original and reviewed read the
// sealed gates.json (a human review does not recompute gates), as does an
// index that cannot read regrades. A run the regrade does not list keeps its
// sealed gates.
func GateSummariesFor(ctx context.Context, index ReadIndex, expID, gradeSource string, runs []RunView, catalog *schemas.Catalog) ([]schemas.GateSummary, error) {
	ri, ok := index.(RegradeIndex)
	if !ok || gradeSource == "" || gradeSource == GradeSourceOriginal || gradeSource == schemas.GradeSourceReviewed {
		return GateSummaries(index, runs, catalog)
	}
	sealed, err := ri.RegradedRuns(ctx, expID, gradeSource)
	if err != nil {
		return nil, err
	}
	byRun := make(map[string][]schemas.GateResult, len(sealed))
	for _, sr := range sealed {
		if sr.Run.RunID != "" {
			byRun[sr.Run.RunID] = sr.Gates
		}
	}
	return gateSummaries(runs, catalog, func(runID string) ([]schemas.GateResult, error) {
		if g, ok := byRun[runID]; ok {
			return g, nil
		}
		var results []schemas.GateResult
		_, err := readJSONFile(index, runID, FileGates, &results)
		return results, err
	})
}

// gateSummaries aggregates the gates gatesOf returns for each counted run.
func gateSummaries(runs []RunView, catalog *schemas.Catalog, gatesOf func(runID string) ([]schemas.GateResult, error)) ([]schemas.GateSummary, error) {
	baseline := map[string]bool{}
	if catalog != nil {
		for _, g := range catalog.Gates {
			baseline[g.Name] = g.Baseline
		}
	}
	counted := countedRuns(runs)
	views := make([]RunView, 0, len(counted))
	for _, rv := range counted {
		views = append(views, rv)
	}
	slices.SortFunc(views, func(x, y RunView) int {
		return cmp.Or(cmp.Compare(x.Slot.OrderIndex, y.Slot.OrderIndex), cmp.Compare(x.Slot.SlotID, y.Slot.SlotID))
	})
	byGate := map[string]*schemas.GateSummary{}
	sealed := 0
	for _, rv := range views {
		if !rv.Slot.Counted || !rv.Sealed {
			continue
		}
		sealed++
		results, err := gatesOf(rv.Run.RunID)
		if err != nil {
			return nil, err
		}
		for _, r := range results {
			gs, ok := byGate[r.Gate]
			if !ok {
				gs = &schemas.GateSummary{Gate: r.Gate, Version: r.Version, Effect: r.Effect, Baseline: baseline[r.Gate]}
				byGate[r.Gate] = gs
			}
			gs.Fired.Den++
			if r.Fired {
				gs.Fired.Num++
				if gs.Detail == "" {
					gs.Detail = "first fired on run " + rv.Run.RunID
					if r.Detail != "" {
						gs.Detail += ": " + r.Detail
					}
				}
			}
		}
	}
	out := make([]schemas.GateSummary, 0, len(byGate))
	for _, gs := range byGate {
		gs.Fired.Unknown = sealed - gs.Fired.Den
		out = append(out, *gs)
	}
	slices.SortFunc(out, func(x, y schemas.GateSummary) int { return cmp.Compare(x.Gate, y.Gate) })
	return out, nil
}
