package handlers

import (
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// U3: the tiers of a suite on GET /suites (docs/suite-tiers.md). The
// resolution matches suite.TierTasks, which the planner uses, without
// importing the suite package: a tier that lists a task outside the suite or
// selects nothing carries the reason planning would refuse it.

// tierOrder is the display order of the built-in tier names.
var tierOrder = map[string]int{"pr": 0, "nightly": 1, "headroom": 2}

// suiteTiers lists s's tiers, nightly included when suite.yaml leaves it out.
func suiteTiers(s *schemas.Suite) []schemas.SuiteTier {
	names := make([]string, 0, len(s.Tiers)+1)
	for name := range s.Tiers {
		names = append(names, name)
	}
	if _, ok := s.Tiers["nightly"]; !ok {
		names = append(names, "nightly")
	}
	sort.Slice(names, func(i, j int) bool {
		oi, iok := tierOrder[names[i]]
		oj, jok := tierOrder[names[j]]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		default:
			return names[i] < names[j]
		}
	})
	out := make([]schemas.SuiteTier, 0, len(names))
	for _, name := range names {
		sel, defined := s.Tiers[name]
		if !defined {
			sel = schemas.TaskSelector{All: true}
		}
		out = append(out, resolveTier(s, name, sel, defined))
	}
	return out
}

// resolveTier resolves one tier's selector against the suite's tasks.
func resolveTier(s *schemas.Suite, name string, sel schemas.TaskSelector, defined bool) schemas.SuiteTier {
	tier := schemas.SuiteTier{Name: name, Tasks: []string{}, Defined: defined}
	known := make(map[string]bool, len(s.TaskIDs))
	for _, id := range s.TaskIDs {
		known[id] = true
	}
	in := map[string]bool{}
	for _, id := range sel.Resolve(s.TaskIDs) {
		if !known[id] {
			tier.Error = "lists task " + id + ", which is not a task of suite " + s.ID
			return tier
		}
		in[id] = true
	}
	for _, id := range s.TaskIDs {
		if in[id] {
			tier.Tasks = append(tier.Tasks, id)
		}
	}
	if len(tier.Tasks) == 0 {
		tier.Error = "selects no tasks"
	}
	return tier
}
