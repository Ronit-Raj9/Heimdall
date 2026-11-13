package handlers

import (
	"cmp"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// GateSummaries aggregates every gate over the sealed latest runs of counted
// slots, from their gates.json. Fired is runs fired over runs checked; a run
// sealed without that gate's result counts as unknown. Baseline comes from the
// catalog when one is given. Gates are sorted by name.
func GateSummaries(index ReadIndex, runs []RunView, catalog *schemas.Catalog) ([]schemas.GateSummary, error) {
	baseline := map[string]bool{}
	if catalog != nil {
		for _, g := range catalog.Gates {
			baseline[g.Name] = g.Baseline
		}
	}
	latest := latestRuns(runs)
	views := make([]RunView, 0, len(latest))
	for _, rv := range latest {
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
		var results []schemas.GateResult
		if _, err := readJSONFile(index, rv.Run.RunID, FileGates, &results); err != nil {
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
