package verdict

import (
	"fmt"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Round 6 N6: "Extend" adds tasks or tries to a finished comparison as a
// follow-up experiment. Its runs are pooled with the parent's at read time,
// from the stages' own analyses, so nothing sealed is rewritten. A follow-up
// joins the pool only when it repeats the parent's comparison: same arm
// configs, same benchmark version and evaluator, same primary endpoint and
// alpha, and only the change its kind allows (fresh tasks or more tries of
// the parent's tasks). Otherwise it is listed with the reason.

// PoolStage is one experiment offered to the pool: its manifest, its
// analysis (nil when none yet) and how many counted runs it sealed.
type PoolStage struct {
	Manifest *schemas.Manifest
	Analysis *schemas.Analysis
	Runs     int
}

// PoolMember is one follow-up of the parent, pooled or not.
type PoolMember struct {
	ExpID  string `json:"exp_id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Runs   int    `json:"runs"`
	Pooled bool   `json:"pooled"`
	Reason string `json:"reason,omitempty"` // why it is not pooled
}

// PooledResult is the primary effect over the parent and its pooled
// follow-ups: per task the arms' means over every run of that task in any
// stage (a task is counted once), d = candidate minus baseline, and the
// paired-t 95% range over those per-task differences.
type PooledResult struct {
	Condition string      `json:"condition"`
	Metric    string      `json:"metric"`
	Tasks     int         `json:"tasks"`
	Runs      int         `json:"runs"`       // counted runs of every pooled stage
	AddedRuns int         `json:"added_runs"` // runs that came from pooled follow-ups
	MeanA     float64     `json:"mean_a"`
	MeanB     float64     `json:"mean_b"`
	DBar      float64     `json:"d_bar"`
	Interval  *[2]float64 `json:"interval,omitempty"`
}

// PoolExtensions pools parent with the follow-ups among kids that extend it.
// The result is nil when no follow-up could be pooled.
func PoolExtensions(parent PoolStage, kids []PoolStage) (*PooledResult, []PoolMember) {
	members := make([]PoolMember, 0, len(kids))
	pooledStages := []PoolStage{parent}
	for _, k := range kids {
		if k.Manifest == nil {
			continue
		}
		mem := PoolMember{ExpID: k.Manifest.ExpID, Name: k.Manifest.Experiment.Name, Runs: k.Runs}
		if ref := k.Manifest.Experiment.Extends; ref != nil {
			mem.Kind = ref.Kind
		}
		if err := poolable(parent, k); err != nil {
			mem.Reason = err.Error()
		} else {
			mem.Pooled = true
			pooledStages = append(pooledStages, k)
		}
		members = append(members, mem)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ExpID < members[j].ExpID })
	if len(pooledStages) == 1 {
		return nil, members
	}
	return poolEffects(pooledStages), members
}

func poolable(parent, child PoolStage) error {
	p, c := parent.Manifest, child.Manifest
	if p == nil {
		return fmt.Errorf("the parent comparison has no plan")
	}
	ref := c.Experiment.Extends
	if ref == nil || ref.ExpID != p.ExpID {
		return fmt.Errorf("%s does not extend %s", c.ExpID, p.ExpID)
	}
	if ref.ManifestHash != p.Hash {
		return fmt.Errorf("%s extends an earlier plan of %s", c.ExpID, p.ExpID)
	}
	pc, cc := settingsFor(p), settingsFor(c)
	if pc.primary != cc.primary {
		return fmt.Errorf("main measure differs: %s/%s and %s/%s", pc.primary.Condition, pc.primary.Metric, cc.primary.Condition, cc.primary.Metric)
	}
	if err := sameComparison(p, c, pc, cc, ref.Kind); err != nil {
		return err
	}
	if primaryEffect(parent.Analysis) == nil {
		return fmt.Errorf("%s has no result yet", p.ExpID)
	}
	if primaryEffect(child.Analysis) == nil {
		return fmt.Errorf("%s has no result yet", c.ExpID)
	}
	return nil
}

func primaryEffect(a *schemas.Analysis) *schemas.Effect {
	if a == nil {
		return nil
	}
	return a.Verdict.Effect
}

type poolCell struct {
	sumA, sumB float64
	nA, nB     int
}

func poolEffects(stages []PoolStage) *PooledResult {
	first := primaryEffect(stages[0].Analysis)
	out := &PooledResult{Condition: first.Condition, Metric: first.Metric}
	per := map[string]*poolCell{}
	for i, s := range stages {
		out.Runs += s.Runs
		if i > 0 {
			out.AddedRuns += s.Runs
		}
		for _, td := range primaryEffect(s.Analysis).PerTask {
			c := per[td.Task]
			if c == nil {
				c = &poolCell{}
				per[td.Task] = c
			}
			c.sumA += td.MeanA * float64(td.NA)
			c.sumB += td.MeanB * float64(td.NB)
			c.nA += td.NA
			c.nB += td.NB
		}
	}
	tasks := make([]string, 0, len(per))
	for t, c := range per {
		if c.nA > 0 && c.nB > 0 {
			tasks = append(tasks, t)
		}
	}
	sort.Strings(tasks)
	d := make([]float64, 0, len(tasks))
	for _, t := range tasks {
		c := per[t]
		a, b := c.sumA/float64(c.nA), c.sumB/float64(c.nB)
		out.MeanA += a
		out.MeanB += b
		d = append(d, b-a)
	}
	out.Tasks = len(tasks)
	if out.Tasks == 0 {
		return out
	}
	n := float64(out.Tasks)
	out.MeanA /= n
	out.MeanB /= n
	out.DBar = out.MeanB - out.MeanA
	if lo, hi, ok := stats.PairedT(d, 0.95); ok && finite(lo) && finite(hi) {
		out.Interval = &[2]float64{lo, hi}
	}
	return out
}
