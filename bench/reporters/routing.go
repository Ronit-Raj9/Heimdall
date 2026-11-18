package reporters

import (
	"bufio"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// writeRouting writes the Gateway policy section (W9, docs/routing-experiments.md)
// for an experiment whose arms declare a routing policy: one row per arm with
// its policy and frozen policy hash, then the lines on which the arms'
// policies differ, so the report describes the arms by their policy
// difference. The per-target metrics (fallback_used, served_model_switches,
// routed_target_*) are in the diagnostics and on the verdict page's card.
// Nothing is written for an experiment of fixed keys.
func writeRouting(w *bufio.Writer, m *schemas.Manifest) {
	if m == nil || len(m.Routing) == 0 {
		return
	}
	ids := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	arms := make([]schemas.RoutingArm, 0, len(ids))
	w.WriteString("## Gateway policy\n\n| Arm | Policy | Policy hash |\n|---|---|---|\n")
	for _, id := range ids {
		a := m.Arms[id]
		arms = append(arms, schemas.RoutingArm{Arm: id, Model: a.Config.Model, Policy: a.Config.Gateway.Routing})
		hash := "-"
		if h := m.Routing[id]; h != "" {
			hash = "`" + shortHash(h) + "`"
		}
		w.WriteString("| " + id + " | " + schemas.RoutingSummaryLine(&a.Config) + " | " + hash + " |\n")
	}
	w.WriteString("\n")
	if diffs := schemas.RoutingDifferences(arms); len(diffs) > 0 {
		w.WriteString("Arms differ in:\n\n")
		for _, d := range diffs {
			w.WriteString("- " + d + "\n")
		}
		w.WriteString("\n")
	}
}
