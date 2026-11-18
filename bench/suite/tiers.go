package suite

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// W7 suite tiers (docs/suite-tiers.md). A suite may name task subsets in
// suite.yaml `tiers:`; an experiment picks one with `tier:` and `tasks: all`
// then means the tier's tasks.

// Built-in tier names. nightly and all mean every task when the suite does
// not define them.
const (
	TierPR       = "pr"
	TierNightly  = "nightly"
	TierHeadroom = "headroom"
	TierAll      = "all"
)

// TierTasks returns the tier's task ids in suite order. A tier the suite does
// not define is an error, except nightly and all, which mean every task.
func TierTasks(s *schemas.Suite, name string) ([]string, error) {
	sel, ok := s.Tiers[name]
	if !ok {
		if name == TierNightly || name == TierAll {
			return append([]string(nil), s.TaskIDs...), nil
		}
		defined := make([]string, 0, len(s.Tiers))
		for k := range s.Tiers {
			defined = append(defined, k)
		}
		sort.Strings(defined)
		return nil, fmt.Errorf("suite %s has no tier %q (defined: %s); `heimdall suite tiers suggest %s` proposes one",
			s.ID, name, strings.Join(append(defined, TierNightly+" (all tasks)"), ", "), s.ID)
	}
	ids := sel.Resolve(s.TaskIDs)
	known := map[string]bool{}
	for _, t := range s.TaskIDs {
		known[t] = true
	}
	in := map[string]bool{}
	for _, t := range ids {
		if !known[t] {
			return nil, fmt.Errorf("tier %s lists task %s, which is not a task of suite %s", name, t, s.ID)
		}
		in[t] = true
	}
	var out []string
	for _, t := range s.TaskIDs {
		if in[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tier %s of suite %s selects no tasks", name, s.ID)
	}
	return out, nil
}

// CompareTiers checks that two manifests may be compared task for task. Two
// manifests planned in different tiers (no tier counts as all tasks) select
// different tasks, so a comparison of their aggregates would compare
// different suites: it is refused, unless restrict asks for the comparison
// on the tasks both ran, which are returned (sorted). Same tier: nil, nil.
func CompareTiers(a, b *schemas.Manifest, restrict bool) ([]string, error) {
	ta, tb := ManifestTier(a), ManifestTier(b)
	if ta == tb {
		return nil, nil
	}
	if !restrict {
		return nil, fmt.Errorf("experiments %s (tier %s) and %s (tier %s) ran different task tiers; compare them only on their common tasks",
			a.ExpID, ta, b.ExpID, tb)
	}
	common := commonTasks(a, b)
	if len(common) == 0 {
		return nil, fmt.Errorf("experiments %s (tier %s) and %s (tier %s) share no task", a.ExpID, ta, b.ExpID, tb)
	}
	return common, nil
}

// ManifestTier is the tier a manifest was planned in, "all" for none.
func ManifestTier(m *schemas.Manifest) string {
	switch {
	case m == nil:
		return TierAll
	case m.Tier != nil && m.Tier.Name != "":
		return m.Tier.Name
	case m.Experiment.Tier != "":
		return m.Experiment.Tier
	default:
		return TierAll
	}
}

func commonTasks(a, b *schemas.Manifest) []string {
	in := map[string]bool{}
	for _, sl := range a.Slots {
		if sl.Counted {
			in[sl.Task] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, sl := range b.Slots {
		if sl.Counted && in[sl.Task] && !seen[sl.Task] {
			seen[sl.Task] = true
			out = append(out, sl.Task)
		}
	}
	sort.Strings(out)
	return out
}

// TierCandidate is one task's evidence for `suite tiers suggest`.
type TierCandidate struct {
	Task           string   `json:"task"`
	PassRate       *float64 `json:"pass_rate"`      // pooled observed pass rate; nil when unknown
	Discrimination string   `json:"discrimination"` // taskhealth label
	Spread         *float64 `json:"spread"`         // best minus worst config pass rate
	Flaky          bool     `json:"flaky"`          // verifier flakiness evidence: never in a tier that gates
	Quarantined    bool     `json:"quarantined"`
	WallS          *float64 `json:"wall_s"` // median observed run wall time; nil when no run
}

// TierSuggestion is the output of SuggestTiers.
type TierSuggestion struct {
	Suite      string                          `json:"suite"`
	MaxMinutes float64                         `json:"max_minutes"`
	Trials     int                             `json:"trials"`
	Arms       int                             `json:"arms"`
	Workers    int                             `json:"workers"`
	Tiers      map[string]schemas.TaskSelector `json:"tiers"`
	PRMinutes  *float64                        `json:"pr_minutes"` // estimated wall clock of the pr tier
	Reasons    map[string]string               `json:"reasons"`    // task -> why it is in or out of pr / headroom
	Notes      []string                        `json:"notes"`
}

// SuggestOptions bound the pr tier.
type SuggestOptions struct {
	MaxMinutes float64 // wall-clock budget of one pr run (default 10)
	MaxTasks   int     // pr size cap (default 10)
	Trials     int     // trials per arm the pr preset runs (default 1)
	Arms       int     // arms (default 2)
	Workers    int     // concurrent runs (default 4)
	Headroom   float64 // headroom: observed pass rate below this (default 0.5)
}

// SuggestTiers proposes pr, nightly and headroom from task health and
// observed wall time. pr takes the most discriminating fast tasks that fit
// MaxMinutes of wall clock (tasks x trials x arms x median wall time over
// workers), never a flaky or quarantined one, and never a task with no
// observed wall time when MaxMinutes is set. nightly is every task. headroom
// is every task whose observed pass rate is below Headroom. It is pure.
func SuggestTiers(suiteID string, cands []TierCandidate, o SuggestOptions) TierSuggestion {
	if o.MaxMinutes <= 0 {
		o.MaxMinutes = 10
	}
	if o.MaxTasks <= 0 {
		o.MaxTasks = 10
	}
	if o.Trials <= 0 {
		o.Trials = 1
	}
	if o.Arms <= 0 {
		o.Arms = 2
	}
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if o.Headroom <= 0 {
		o.Headroom = 0.5
	}
	out := TierSuggestion{Suite: suiteID, MaxMinutes: o.MaxMinutes, Trials: o.Trials, Arms: o.Arms, Workers: o.Workers,
		Tiers: map[string]schemas.TaskSelector{TierNightly: {All: true}}, Reasons: map[string]string{}}
	var pool []TierCandidate
	var headroom []string
	for _, c := range cands {
		switch {
		case c.Quarantined:
			out.Reasons[c.Task] = "quarantined: in no tier but nightly's record"
			continue
		case c.Flaky:
			out.Reasons[c.Task] = "flaky verifier: never in a gating tier"
			continue
		}
		if c.PassRate != nil && *c.PassRate < o.Headroom {
			headroom = append(headroom, c.Task)
		}
		if c.WallS == nil {
			out.Reasons[c.Task] = "no observed wall time: cannot be fitted into the pr budget"
			continue
		}
		pool = append(pool, c)
	}
	sort.SliceStable(pool, func(i, j int) bool {
		si, sj := discriminationScore(pool[i]), discriminationScore(pool[j])
		if si != sj {
			return si > sj
		}
		return *pool[i].WallS < *pool[j].WallS
	})
	budget := o.MaxMinutes * 60 * float64(o.Workers)
	used := 0.0
	var pr []string
	for _, c := range pool {
		cost := *c.WallS * float64(o.Trials*o.Arms)
		if len(pr) >= o.MaxTasks {
			out.Reasons[c.Task] = fmt.Sprintf("pr is full at %d tasks", o.MaxTasks)
			continue
		}
		if used+cost > budget {
			out.Reasons[c.Task] = fmt.Sprintf("would pass the %.0f-minute pr budget", o.MaxMinutes)
			continue
		}
		if discriminationScore(c) <= 0 {
			out.Reasons[c.Task] = "not discriminating (always passes or always fails for every config): carries no information in a PR gate"
			continue
		}
		used += cost
		pr = append(pr, c.Task)
		out.Reasons[c.Task] = fmt.Sprintf("pr: %s, median %.0f s per run", labelOf(c), *c.WallS)
	}
	sort.Strings(pr)
	sort.Strings(headroom)
	if len(pr) > 0 {
		out.Tiers[TierPR] = schemas.TaskSelector{IDs: pr}
		mins := math.Ceil(used/float64(o.Workers)/60*10) / 10
		out.PRMinutes = &mins
	} else {
		out.Notes = append(out.Notes, "no task qualifies for pr: run the suite (nightly) a few times first, so health has pass rates, configs and wall times")
	}
	if len(pr) > 0 && len(pr) < 6 {
		out.Notes = append(out.Notes, fmt.Sprintf("pr has %d tasks: fewer than 6 discordant tasks can never reach p < 0.05, so a pr verdict can only be INCONCLUSIVE or INVALID; it still catches broken runs and hard regressions through guards and gates", len(pr)))
	}
	if len(headroom) > 0 {
		out.Tiers[TierHeadroom] = schemas.TaskSelector{IDs: headroom}
	}
	return out
}

// discriminationScore ranks a task for pr: discriminating tasks first, by
// spread between configs, then tasks whose pass rate is near 0.5 (most
// variance), then unknown ones; never-informative tasks score 0.
func discriminationScore(c TierCandidate) float64 {
	switch c.Discrimination {
	case "always_pass", "always_fail":
		return 0
	case "discriminating":
		s := 2.0
		if c.Spread != nil {
			s += *c.Spread
		}
		return s
	}
	if c.PassRate != nil {
		p := *c.PassRate
		if p <= 0 || p >= 1 {
			return 0
		}
		return 1 + p*(1-p)
	}
	return 0.5
}

func labelOf(c TierCandidate) string {
	parts := []string{}
	if c.Discrimination != "" {
		parts = append(parts, c.Discrimination)
	}
	if c.PassRate != nil {
		parts = append(parts, fmt.Sprintf("pass rate %.2f", *c.PassRate))
	}
	if len(parts) == 0 {
		return "no health data"
	}
	return strings.Join(parts, ", ")
}
