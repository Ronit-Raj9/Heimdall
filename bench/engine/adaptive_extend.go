package engine

import (
	"fmt"
	"math/rand/v2"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
)

// W7 buy more evidence: `extend` turns an inconclusive experiment into a
// follow-up experiment, a new manifest of its own, linked to the parent.
// With -tasks N the follow-up runs N tasks the parent never ran, drawn from
// the parent's seed so nobody picks them; with -trials K it runs K more
// trials of the parent's tasks. Only the decision conditions (the primary
// and the no_regression guards) are kept: the follow-up exists to decide.

// Extension kinds.
const (
	ExtendTasks  = "tasks"
	ExtendTrials = "trials"
)

// extendStream is the second PCG word of the follow-up task draw.
const extendStream = 0x65787464 // "extd"

// ExtendExperiment builds the follow-up experiment of parent. It returns
// notes worth printing (fewer tasks than asked, conditions dropped).
func ExtendExperiment(parent *schemas.Manifest, s *schemas.Suite, kind string, n int) (*schemas.Experiment, []string, error) {
	if parent == nil || s == nil {
		return nil, nil, fmt.Errorf("extend: nil manifest or suite")
	}
	if n < 1 {
		return nil, nil, fmt.Errorf("extend: need -tasks N or -trials K with a value >= 1")
	}
	if parent.Experiment.Extends != nil {
		return nil, nil, fmt.Errorf("extend: %s is itself a follow-up of %s; extend the first experiment's analysis instead", parent.ExpID, parent.Experiment.Extends.ExpID)
	}
	pe := parent.Experiment
	e := pe
	e.ID = ""
	e.Name = pe.Name + "-ext"
	e.Seed = pe.Seed + 1
	e.Matrix = nil
	e.Compatibility = nil
	rule := schemas.ExtensionExploratory
	if _, ok := verdict.ExtensionAlphaFirst(parent, alphaOf(pe)); ok {
		rule = schemas.ExtensionInverseNormal
	}
	e.Extends = &schemas.ExtendsRef{ExpID: parent.ExpID, ManifestHash: parent.Hash, Kind: kind, Rule: rule}
	// The follow-up is a fixed design, or the parent's task rule; it declares
	// no extension of its own and keeps the parent's verdict alpha.
	e.Trials = nil
	if t := pe.Trials; t != nil && t.Mode == schemas.TrialsAdaptive && t.Rule == schemas.AdaptiveTask && kind == ExtendTasks {
		cp := *t
		cp.Extension = nil
		e.Trials = &cp
	}
	keep := map[string]bool{pe.Primary.Condition: true}
	for _, g := range pe.Guards {
		if g.Rule == GuardNoRegression {
			keep[g.Condition] = true
		}
	}
	if len(pe.Guards) == 0 {
		keep[schemas.ConditionClean] = true
	}
	used := map[string]bool{}
	perCond := map[string][]string{}
	trials := map[string]int{}
	for _, sl := range parent.Slots {
		if !sl.Counted {
			continue
		}
		used[sl.Task] = true
		if !contains(perCond[sl.Condition], sl.Task) {
			perCond[sl.Condition] = append(perCond[sl.Condition], sl.Task)
		}
		trials[sl.Condition] = max(trials[sl.Condition], sl.Trial)
	}
	var notes []string
	var conds []schemas.ConditionSpec
	switch kind {
	case ExtendTasks:
		// New tasks come from the parent's split only, so a public
		// experiment never grows into held-out tasks.
		split := schemas.SplitPublic
		for _, c := range pe.Conditions {
			if c.ID == pe.Primary.Condition && c.Split != "" {
				split = c.Split
			}
		}
		var pool []string
		for _, t := range s.TaskIDs {
			if !used[t] && taskSplit(s.TaskByID(t)) == split {
				pool = append(pool, t)
			}
		}
		r := rand.New(rand.NewPCG(uint64(pe.Seed), extendStream))
		r.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		if len(pool) < n {
			notes = append(notes, fmt.Sprintf("asked for %d new tasks, the suite has only %d the parent did not run", n, len(pool)))
			n = len(pool)
		}
		if n == 0 {
			return nil, notes, fmt.Errorf("extend: suite %s has no task that %s did not run; use -trials K, or add tasks", s.ID, parent.ExpID)
		}
		fresh := append([]string(nil), pool[:n]...)
		sort.Strings(fresh)
		e.Tier = "" // the new tasks lie outside the parent's tier by construction
		for _, c := range pe.Conditions {
			if !keep[c.ID] {
				notes = append(notes, fmt.Sprintf("condition %s is descriptive and not repeated", c.ID))
				continue
			}
			c.Tasks = schemas.TaskSelector{IDs: fresh}
			conds = append(conds, c)
		}
	case ExtendTrials:
		for _, c := range pe.Conditions {
			if !keep[c.ID] {
				notes = append(notes, fmt.Sprintf("condition %s is descriptive and not repeated", c.ID))
				continue
			}
			tasks := append([]string(nil), perCond[c.ID]...)
			sort.Strings(tasks)
			c.Tasks = schemas.TaskSelector{IDs: tasks}
			c.Trials = n
			conds = append(conds, c)
		}
	default:
		return nil, nil, fmt.Errorf("extend: kind %q: want tasks or trials", kind)
	}
	e.Conditions = conds
	// The copied task rule may name conditions the follow-up does not
	// repeat; planning rejects an unknown adaptive condition, so the list
	// keeps only the retained ones, and a rule left with none becomes a
	// fixed design (audit qc2-03-02).
	if t := e.Trials; t != nil && len(t.Conditions) > 0 {
		var ids []string
		for _, id := range t.Conditions {
			for _, c := range conds {
				if c.ID == id {
					ids = append(ids, id)
					break
				}
			}
		}
		if len(ids) == 0 {
			e.Trials = nil
			notes = append(notes, "no condition of the parent's adaptive trials rule is repeated, so the follow-up runs every planned trial")
		} else if len(ids) < len(t.Conditions) {
			cp := *t
			cp.Conditions = ids
			e.Trials = &cp
		}
	}
	if rule == schemas.ExtensionExploratory {
		notes = append(notes, fmt.Sprintf("%s pre-declared no combination rule (trials.extension): the follow-up decides on its own, and the pooled analysis with %s is exploratory", parent.ExpID, parent.ExpID))
	}
	return &e, notes, nil
}

func alphaOf(e schemas.Experiment) float64 {
	if a := e.Verdict.Alpha; a > 0 && a < 1 {
		return a
	}
	return 0.05
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
