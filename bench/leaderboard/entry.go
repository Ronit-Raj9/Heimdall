package leaderboard

import (
	"fmt"
	"math"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Metric ids of the entry's metrics map. The run-level ones are the ids in
// each run's metrics.json (metrics/builtin.go).
const (
	MetricSteps           = "steps"
	MetricToolCalls       = "tool_calls"
	MetricTokens          = "tokens" // fresh + cache read + cache write + output, per run
	MetricCostUSD         = "cost_usd"
	MetricWallTime        = "wall_time_s"
	MetricFalseCompletion = "false_completion"
	MetricSafeSuccess     = "safe_success"
	MetricCostPerSuccess  = "cost_per_success"
)

// medianMetrics are aggregated as the median over runs, rateMetrics as the
// mean of the 0/1 values over runs.
var (
	medianMetrics = []string{MetricSteps, MetricToolCalls, MetricTokens, MetricCostUSD, MetricWallTime}
	rateMetrics   = []string{MetricFalseCompletion, MetricSafeSuccess}
)

// metricEvidence is the level an aggregate reports when no run observed it.
var metricEvidence = map[string]schemas.EvidenceLevel{
	MetricSteps: schemas.EvidenceGateway, MetricToolCalls: schemas.EvidenceGateway, MetricTokens: schemas.EvidenceGateway,
	MetricCostUSD: schemas.EvidenceGateway, MetricWallTime: schemas.EvidenceOutcome,
	MetricFalseCompletion: schemas.EvidenceNative, MetricSafeSuccess: schemas.EvidenceOutcome,
}

// successConf is the level of the descriptive interval on Success.
const successConf = 0.95

// entryCalc is an entry under construction with its per-task success rates
// in scope, which ranking and tiers read.
type entryCalc struct {
	e     schemas.LeaderboardEntry
	rates map[string]float64
}

// taskTally is one task's verified successes over graded runs.
type taskTally struct{ succ, n int }

// buildEntries makes one entry per config with a graded run under the
// board's condition, unranked and ordered by config hash, and returns the
// common tasks: the tasks every entry ran. With scope common every entry is
// restricted to them; with scope all they are only reported.
func buildEntries(p pool, q Query) ([]*entryCalc, []string) {
	var hashes []string
	for h, byCond := range p {
		if len(byCond[q.Condition]) > 0 {
			hashes = append(hashes, h)
		}
	}
	slices.Sort(hashes)
	var common []string
	for i, h := range hashes {
		tasks := taskSet(p[h][q.Condition])
		if i == 0 {
			common = tasks
			continue
		}
		common = slices.DeleteFunc(common, func(t string) bool { return !slices.Contains(tasks, t) })
	}
	if common == nil {
		common = []string{}
	}
	out := make([]*entryCalc, 0, len(hashes))
	for _, h := range hashes {
		all := p[h][q.Condition]
		scoped := all
		if q.Scope == ScopeCommon {
			scoped = slices.DeleteFunc(slices.Clone(all), func(r gradedRun) bool { return !slices.Contains(common, r.rec.slot.Task) })
		}
		c := newEntry(h, all, scoped)
		if q.Condition == schemas.ConditionClean {
			c.e.Resilience = resilience(p[h], c.rates)
		}
		out = append(out, c)
	}
	return out, common
}

// newEntry computes an entry's numbers from its runs in scope. Identity
// (agent, model, label) comes from the newest experiment among all its runs
// under the condition, so an entry restricted to no tasks still says who it is.
func newEntry(hash string, all, scoped []gradedRun) *entryCalc {
	c := &entryCalc{rates: map[string]float64{}}
	e := &c.e
	e.ConfigHash = hash
	identity := all[0].rec
	for _, r := range all {
		if newestFirst(r.rec.exp, identity.exp) < 0 {
			identity = r.rec
		}
	}
	if identity.arm != nil {
		e.Agent, e.Model, e.Label = identity.arm.Config.ID, identity.arm.Config.Model, identity.arm.Label
	} else {
		e.Agent = identity.run.Agent.ConfigID
	}
	recs := make([]*record, 0, len(scoped))
	tally := map[string]taskTally{}
	for _, r := range scoped {
		recs = append(recs, r.rec)
		t := tally[r.rec.slot.Task]
		t.n++
		if r.success == 1 {
			t.succ++
			e.Passes.Num++
		}
		tally[r.rec.slot.Task] = t
	}
	e.Experiments = experimentsNewestFirst(recs)
	e.Tasks, e.Runs, e.Passes.Den = len(tally), len(scoped), len(scoped)
	for task, t := range tally {
		c.rates[task] = float64(t.succ) / float64(t.n)
		if e.Trials == 0 || t.n < e.Trials {
			e.Trials = t.n
		}
	}
	e.Success = successValue(c.rates)
	e.Reliability = reliability(tally, e.Trials)
	e.Metrics = runMetrics(scoped)
	e.Resilience = map[string]schemas.Value{}
	return c
}

// successValue is the mean over tasks of the per-task success rate, with the
// descriptive paired-t interval over tasks clipped to [0, 1].
func successValue(rates map[string]float64) schemas.Value {
	if len(rates) == 0 {
		return schemas.Unknown("no graded run in scope", schemas.EvidenceOutcome)
	}
	xs := sortedValues(rates)
	v := schemas.Observed(stats.Mean(xs), schemas.EvidenceOutcome)
	lo, hi, ok := stats.PairedT(xs, successConf)
	if !ok {
		v.Reason = "one task: no interval"
		return v
	}
	v.Bounds = &[2]float64{math.Max(0, lo), math.Min(1, hi)}
	return v
}

// reliability is the mean over tasks of pass^k with k the fewest graded runs
// on any task: the chance that k tries in a row all succeed.
func reliability(tally map[string]taskTally, k int) schemas.Value {
	if k < 2 {
		return schemas.Unknown(fmt.Sprintf("needs 2 or more graded runs on every task; the fewest is %d", k), schemas.EvidenceOutcome)
	}
	tasks := make([]string, 0, len(tally))
	for t := range tally {
		tasks = append(tasks, t)
	}
	slices.Sort(tasks)
	xs := make([]float64, 0, len(tasks))
	for _, t := range tasks {
		p, ok := stats.PassHatK(tally[t].succ, tally[t].n, k)
		if !ok {
			return schemas.Unknown(fmt.Sprintf("pass^%d undefined on task %s", k, t), schemas.EvidenceOutcome)
		}
		xs = append(xs, p)
	}
	v := schemas.Observed(stats.Mean(xs), schemas.EvidenceOutcome)
	v.Reason = fmt.Sprintf("pass^%d", k)
	return v
}

// runMetrics aggregates the per-run metrics of an entry.
func runMetrics(runs []gradedRun) map[string]schemas.Value {
	out := make(map[string]schemas.Value, len(medianMetrics)+len(rateMetrics)+1)
	for _, id := range medianMetrics {
		out[id] = aggregate(id, runs, stats.Median)
	}
	for _, id := range rateMetrics {
		out[id] = aggregate(id, runs, stats.Mean)
	}
	out[MetricCostPerSuccess] = costPerSuccess(runs)
	return out
}

// aggregate reduces one metric over runs with f, over the runs that observed
// it. Not applicable values (false completion on a passing run) are left
// out; with nothing else the aggregate is not applicable too. Unknown and
// missing values never count as zero: Reason says how many there were, and
// with no observed value the aggregate is unknown.
func aggregate(id string, runs []gradedRun, f func([]float64) float64) schemas.Value {
	var xs []float64
	unknown, na := 0, 0
	ev := metricEvidence[id]
	for _, r := range runs {
		v, found := r.metrics[id]
		x, observed := v.Float()
		switch {
		case observed && !math.IsNaN(x) && !math.IsInf(x, 0):
			xs = append(xs, x)
			if v.Evidence != "" {
				ev = v.Evidence
			}
		case found && v.State == schemas.ValueNotApplicable:
			na++
		default:
			unknown++
		}
	}
	switch {
	case len(xs) == 0 && unknown == 0 && na > 0:
		return schemas.NotApplicable()
	case len(xs) == 0:
		return schemas.Unknown(fmt.Sprintf("unknown in all %d runs", unknown), ev)
	}
	v := schemas.Observed(f(xs), ev)
	if unknown > 0 {
		v.Reason = fmt.Sprintf("%d of %d runs unknown", unknown, len(xs)+unknown)
	}
	return v
}

// costPerSuccess is the observed cost of every graded run, failures
// included, over the verified successes. Not applicable with no success;
// unknown when any run's cost is unknown, since a partial sum would flatter
// the entry.
func costPerSuccess(runs []gradedRun) schemas.Value {
	var sum float64
	succ, unknown := 0, 0
	for _, r := range runs {
		if r.success == 1 {
			succ++
		}
		v := r.metrics[MetricCostUSD]
		if x, ok := v.Float(); ok && !math.IsNaN(x) && !math.IsInf(x, 0) {
			sum += x
		} else if v.State != schemas.ValueNotApplicable {
			unknown++
		}
	}
	switch {
	case len(runs) == 0:
		return schemas.Unknown("no graded run in scope", schemas.EvidenceGateway)
	case succ == 0:
		v := schemas.NotApplicable()
		v.Evidence, v.Reason = schemas.EvidenceGateway, fmt.Sprintf("undefined (0 of %d)", len(runs))
		return v
	case unknown > 0:
		return schemas.Unknown(fmt.Sprintf("cost unknown in %d of %d runs", unknown, len(runs)), schemas.EvidenceGateway)
	}
	return schemas.Observed(sum/float64(succ), schemas.EvidenceGateway)
}

// resilience maps each other condition the config ran to the mean over
// tasks graded both clean (in scope) and under it of the success rate under
// it minus the clean rate.
func resilience(byCond map[string][]gradedRun, clean map[string]float64) map[string]schemas.Value {
	out := map[string]schemas.Value{}
	for cond, runs := range byCond {
		if cond == schemas.ConditionClean || len(runs) == 0 {
			continue
		}
		tally := map[string]taskTally{}
		for _, r := range runs {
			t := tally[r.rec.slot.Task]
			t.n++
			if r.success == 1 {
				t.succ++
			}
			tally[r.rec.slot.Task] = t
		}
		var tasks []string
		for task := range tally {
			if _, ok := clean[task]; ok {
				tasks = append(tasks, task)
			}
		}
		if len(tasks) == 0 {
			out[cond] = schemas.Unknown("no task graded both clean and under "+cond, schemas.EvidenceOutcome)
			continue
		}
		slices.Sort(tasks)
		d := make([]float64, 0, len(tasks))
		for _, task := range tasks {
			d = append(d, float64(tally[task].succ)/float64(tally[task].n)-clean[task])
		}
		v := schemas.Observed(stats.Mean(d), schemas.EvidenceOutcome)
		v.Reason = fmt.Sprintf("%d task%s", len(tasks), plural(len(tasks)))
		out[cond] = v
	}
	return out
}

// taskSet returns the sorted distinct tasks of runs.
func taskSet(runs []gradedRun) []string {
	var out []string
	for _, r := range runs {
		if !slices.Contains(out, r.rec.slot.Task) {
			out = append(out, r.rec.slot.Task)
		}
	}
	slices.Sort(out)
	return out
}

// sortedValues returns a map's values ordered by key, so float sums are
// reproducible.
func sortedValues(m map[string]float64) []float64 {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]float64, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}
