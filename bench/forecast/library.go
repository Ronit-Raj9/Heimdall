package forecast

import (
	"fmt"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// LibraryWiden is how much a library estimate widens the history range:
// the low end is divided by it and the high end multiplied by it, because
// another agent's runs say only roughly what this agent will spend.
const LibraryWiden = 1.5

// libraryArm estimates an arm with no history of its own from the library:
// sealed runs of other agents (or other configs) on the same suite version.
// Time is their wall time; cost is their recorded cost repriced from the
// model each run used to the arm's model through in.Reprice (a run on the
// arm's own model needs no repricing; a run on an unpriced model is left
// out). Both ranges are widened by LibraryWiden. ok is false when the
// library has no usable run on the suite, and the caller falls back to the
// budget bound.
func libraryArm(a schemas.ArmForecast, arm schemas.ArmManifest, tasks []string, suite string, in Input) (schemas.ArmForecast, bool) {
	var runs []HistoryRun
	for _, h := range in.Library {
		if h.Suite == suite {
			runs = append(runs, h)
		}
	}
	if len(runs) == 0 {
		return a, false
	}
	target := arm.Config.Model
	var lo, mid, hi []HistoryRun
	for _, h := range runs {
		rl, rm, rh, ok := 1.0, 1.0, 1.0, h.Model == target && target != ""
		if !ok && in.Reprice != nil && target != "" {
			rl, rm, rh, ok = in.Reprice(h.Model, target)
		}
		x, observed := h.CostUSD.Float()
		if !ok || !observed {
			continue
		}
		lo, mid, hi = append(lo, withCost(h, x*rl)), append(mid, withCost(h, x*rm)), append(hi, withCost(h, x*rh))
	}
	wall := slotRanges(tasks, runs, func(h HistoryRun) schemas.Value { return h.WallS })
	tokens := slotRanges(tasks, runs, func(h HistoryRun) schemas.Value { return h.Tokens })
	if wall.total.Kind != schemas.RangeHistory && len(lo) == 0 {
		return a, false
	}
	a.Basis, a.HistoryRuns, a.Experiments = schemas.BasisLibrary, len(runs), experimentsOf(runs)
	a.AgentTimeS, a.Tokens = widen(wall.total), widen(tokens.total)
	a.PerRun = schemas.PerRunRanges{WallS: widen(wall.perRun), Tokens: widen(tokens.perRun)}
	cost := func(h HistoryRun) schemas.Value { return h.CostUSD }
	if len(lo) > 0 {
		l, m, h := slotRanges(tasks, lo, cost), slotRanges(tasks, mid, cost), slotRanges(tasks, hi, cost)
		a.CostUSD = widen(historyRange(*l.total.Low, *m.total.Median, *h.total.High))
		a.PerRun.CostUSD = widen(historyRange(*l.perRun.Low, *m.perRun.Median, *h.perRun.High))
		a.TaskLevel = m.taskLevel
	} else {
		why := "the library runs on this suite used models without a price, so they cannot be repriced for " + target
		a.CostUSD, a.PerRun.CostUSD = unknownRange(why), unknownRange(why)
	}
	a.BasisText = libraryText(runs, len(lo), suite, target)
	return a, true
}

// LibraryBasis reports whether an arm forecast rests on library runs.
func LibraryBasis(a schemas.ArmForecast) bool { return a.Basis == schemas.BasisLibrary }

// withCost is h with its cost replaced by usd.
func withCost(h HistoryRun, usd float64) HistoryRun {
	h.CostUSD = schemas.Observed(usd, schemas.EvidenceGateway)
	return h
}

// widen stretches a history range: low over LibraryWiden, high times
// LibraryWiden, the median kept.
func widen(r schemas.ForecastRange) schemas.ForecastRange {
	if r.Kind != schemas.RangeHistory || r.Low == nil || r.High == nil {
		return r
	}
	lo, hi := *r.Low/LibraryWiden, *r.High*LibraryWiden
	out := r
	out.Low, out.High = &lo, &hi
	return out
}

// libraryText says in plain words what the library estimate rests on.
func libraryText(runs []HistoryRun, priced int, suite, target string) string {
	var agents []string
	for _, h := range runs {
		if h.Agent != "" && !slices.Contains(agents, h.Agent) {
			agents = append(agents, h.Agent)
		}
	}
	slices.Sort(agents)
	if len(agents) > 4 {
		agents = append(agents[:4], fmt.Sprintf("%d more", len(agents)-4))
	}
	who := strings.Join(agents, ", ")
	if who == "" {
		who = "other agents"
	}
	model := target
	if model == "" {
		model = "the agent's own model"
	}
	return fmt.Sprintf("first run, estimate from library data: %d finished run%s of %s on %s (%d with a cost repriced for %s); your agent may differ, so the range is widened by half each way",
		len(runs), plural(len(runs)), who, suite, priced, model)
}
