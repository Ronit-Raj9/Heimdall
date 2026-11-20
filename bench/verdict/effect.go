package verdict

import (
	"math"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Arm ids the paired_signflip policy compares. A is the baseline, B the
// candidate, and A2 an optional second copy of A whose differences against A
// measure the noise the MDE needs.
const (
	ArmBaseline  = "A"
	ArmCandidate = "B"
	ArmAA        = "A2"
)

// Metric names the policy reads from SealedRun.Metrics (DESIGN.md 9.5).
const (
	MetricVerifiedSuccess = "verified_success"
	MetricFalseCompletion = "false_completion"
	MetricClaimPrecision  = "claim_precision"
	MetricSteps           = "steps"
	MetricToolCalls       = "tool_calls"
	MetricTokens          = "tokens"
	MetricCostUSD         = "cost_usd"
	MetricWallTime        = "wall_time_s"
)

// zeroTolerance treats a d_bar this close to zero as no effect to detect.
const zeroTolerance = 1e-9

// zeroRelTol matches stats.SignFlip: a difference within this fraction of the
// largest |d_i| is a tie.
const zeroRelTol = 1e-12

// fill says how a cell's unknown runs are filled when the cell is averaged.
type fill int

const (
	fillObserved fill = iota // drop unknowns: the point estimate on observed runs
	fillLow                  // every unknown at its lowest possible value
	fillHigh                 // every unknown at its highest possible value
)

// cell holds one (task, arm) group of runs within one condition, reduced to
// what the three missingness fills need.
type cell struct {
	n         int     // assigned runs
	nObs      int     // runs with an observed value
	obsSum    float64 // sum of the observed values
	unkLo     float64 // sum of the lowest possible values of the unknowns
	unkHi     float64 // sum of the highest possible values of the unknowns
	unbounded int     // unknowns with no bounds of their own on a metric outside [0, 1]
}

// mean returns the cell's mean under f, and false when the fill leaves no run
// to average.
func (c cell) mean(f fill) (float64, bool) {
	switch f {
	case fillObserved:
		if c.nObs == 0 {
			return 0, false
		}
		return c.obsSum / float64(c.nObs), true
	case fillLow:
		if c.n == 0 {
			return 0, false
		}
		return (c.obsSum + c.unkLo) / float64(c.n), true
	default:
		if c.n == 0 {
			return 0, false
		}
		return (c.obsSum + c.unkHi) / float64(c.n), true
	}
}

// add folds one run's value into the cell. Anything not observed, or observed
// but not finite, is unknown for the effect. An unknown carrying its own
// bounds lies within them. An unknown without bounds lies in [0, 1] only on a
// rate metric (rate); on any other metric (cost, tokens, steps, time) it
// could be anything, so it is counted as unbounded and the effect gets no
// missingness bounds (pairedEffect).
func (c *cell) add(v schemas.Value, rate bool) {
	c.n++
	if x, ok := v.Float(); ok && finite(x) {
		c.nObs++
		c.obsSum += x
		return
	}
	lo, hi := 0.0, 1.0
	switch {
	case v.Bounds != nil:
		lo, hi = v.Bounds[0], v.Bounds[1]
	case !rate:
		c.unbounded++
	}
	c.unkLo += lo
	c.unkHi += hi
}

// runIndex groups runs by condition, task and arm.
type runIndex map[string]map[string]map[string][]schemas.SealedRun

// newRunIndex indexes the counted runs only; compatibility runs never enter
// any paired comparison.
func newRunIndex(runs []schemas.SealedRun) runIndex {
	idx := runIndex{}
	for _, r := range runs {
		if !r.Slot.Counted {
			continue
		}
		byTask, ok := idx[r.Slot.Condition]
		if !ok {
			byTask = map[string]map[string][]schemas.SealedRun{}
			idx[r.Slot.Condition] = byTask
		}
		byArm, ok := byTask[r.Slot.Task]
		if !ok {
			byArm = map[string][]schemas.SealedRun{}
			byTask[r.Slot.Task] = byArm
		}
		byArm[r.Slot.Arm] = append(byArm[r.Slot.Arm], r)
	}
	return idx
}

// cells returns task -> cell for one arm and metric within one condition.
func (idx runIndex) cells(condition, arm, metric string) map[string]cell {
	out := map[string]cell{}
	rate := RateMetric(metric)
	for task, byArm := range idx[condition] {
		runs, ok := byArm[arm]
		if !ok {
			continue
		}
		var c cell
		for _, r := range runs {
			c.add(decisionValue(metricValue(r, metric)), rate)
		}
		out[task] = c
	}
	return out
}

// armRuns returns every run of arm, across conditions when condition is empty.
func (idx runIndex) armRuns(condition, arm string) []schemas.SealedRun {
	var out []schemas.SealedRun
	for _, cond := range sortedKeys(idx) {
		if condition != "" && cond != condition {
			continue
		}
		for _, task := range sortedKeys(idx[cond]) {
			out = append(out, idx[cond][task][arm]...)
		}
	}
	return out
}

// metricValue returns the run's value for metric. A metric the run never
// recorded is unknown, never zero. An outcome-scored metric of a run whose
// final Y is unknown (infra_error after a graded pass, for example) is
// unknown too, so it never votes against the sealed outcome.
func metricValue(r schemas.SealedRun, metric string) schemas.Value {
	v, ok := r.Metrics[metric]
	if !ok {
		return schemas.Unknown("metric "+metric+" not recorded", "")
	}
	return schemas.ReconcileOutcomeMetric(metric, v, r.Run.Outcome)
}

// decisionValue is v as decision arithmetic may use it. A value whose only
// source is the agent's own report is not witnessed evidence, so it is
// unknown for an effect, and with no bounds (unless a rate) the effect can
// never be IMPROVED or REGRESSED on it (extensions-design.md rule 2).
func decisionValue(v schemas.Value) schemas.Value {
	if v.IsObserved() && v.Source == schemas.EvidenceSourceAgentReported {
		return schemas.Unknown("agent-reported value cannot decide a verdict", v.Evidence)
	}
	return v
}

// pairedEffect computes the release effect of armB over armA on tasks where
// both arms ran: the point estimate on observed runs, with the lower bound
// (unknowns set against B) and the upper bound (unknowns set in favour of B)
// attached. "Against B" follows the metric's direction: on a lower-is-better
// metric it is B's unknowns high and A's low. When some paired run has an
// unknown with no bounds on a metric outside [0, 1], the bounds do not exist
// and both are left nil, so the effect can never be IMPROVED or REGRESSED.
// Inferential effects carry the exact sign-flip p-value; descriptive ones
// carry none (DESIGN.md 9.2).
func pairedEffect(condition, metric, armA, armB string, a, b map[string]cell, inferential bool) *schemas.Effect {
	var tasks []string
	unbounded := false
	for task := range a {
		if _, ok := b[task]; ok {
			tasks = append(tasks, task)
			unbounded = unbounded || a[task].unbounded > 0 || b[task].unbounded > 0
		}
	}
	sort.Strings(tasks)
	e := effectAt(condition, metric, armA, armB, tasks, a, b, fillObserved, fillObserved, inferential)
	if unbounded {
		return e
	}
	low := effectAt(condition, metric, armA, armB, tasks, a, b, fillHigh, fillLow, inferential)
	high := effectAt(condition, metric, armA, armB, tasks, a, b, fillLow, fillHigh, inferential)
	if !HigherIsBetter(metric) {
		low, high = high, low
	}
	e.LowerBound, e.UpperBound = low, high
	return e
}

// pairedTasks is how many tasks an effect pairs: every task both arms ran,
// counted at the bounds where unknowns are filled, or the observed estimate's
// count when the effect has no bounds.
func pairedTasks(e *schemas.Effect) int {
	switch {
	case e == nil:
		return 0
	case e.LowerBound != nil:
		return e.LowerBound.NTasks
	default:
		return e.NTasks
	}
}

// effectAt computes one effect with A's unknowns filled by fa and B's by fb.
// Tasks with a zero difference stay in n: they cannot move the sign-flip
// statistic, but dropping them would overstate d_bar.
func effectAt(condition, metric, armA, armB string, tasks []string, a, b map[string]cell, fa, fb fill, inferential bool) *schemas.Effect {
	e := &schemas.Effect{
		Condition:   condition,
		Metric:      metric,
		ArmA:        armA,
		ArmB:        armB,
		PerTask:     []schemas.TaskDiff{},
		Descriptive: !inferential,
	}
	d := make([]float64, 0, len(tasks))
	for _, task := range tasks {
		ca, cb := a[task], b[task]
		ma, okA := ca.mean(fa)
		mb, okB := cb.mean(fb)
		if !okA || !okB {
			continue
		}
		diff := mb - ma
		e.PerTask = append(e.PerTask, schemas.TaskDiff{
			Task: task, MeanA: ma, MeanB: mb, D: diff,
			NA: ca.n, NB: cb.n, UnknownA: ca.n - ca.nObs, UnknownB: cb.n - cb.nObs,
		})
		d = append(d, diff)
	}
	fillEffectStats(e, d, inferential)
	return e
}

// fillEffectStats sets the counts, d_bar, the descriptive interval and, for an
// inferential effect, the exact sign-flip p-value from the per-task vector d.
// An inferential effect takes its counts from the test itself so the
// discordant count shown always matches the p-value.
func fillEffectStats(e *schemas.Effect, d []float64, inferential bool) {
	e.NTasks = len(d)
	e.NPositive, e.NNegative = signCounts(d)
	if len(d) > 0 {
		e.DBar = stats.Mean(d)
	}
	if lo, hi, ok := stats.PairedT(d, 0.95); ok && finite(lo) && finite(hi) {
		e.Interval = &[2]float64{lo, hi}
	}
	if inferential {
		p := 1.0
		if len(d) > 0 {
			r := stats.SignFlip(d)
			if finite(r.P) {
				p = r.P
			}
			e.NPositive, e.NNegative = r.NPositive, r.NNegative
		}
		e.PValue = &p
	}
	e.NNonzero = e.NPositive + e.NNegative
}

// signCounts counts positive and negative differences with the same relative
// zero tolerance as stats.SignFlip, so floating noise is a tie.
func signCounts(d []float64) (pos, neg int) {
	maxAbs := 0.0
	for _, x := range d {
		maxAbs = math.Max(maxAbs, math.Abs(x))
	}
	for _, x := range d {
		switch {
		case math.Abs(x) <= zeroRelTol*maxAbs:
		case x > 0:
			pos++
		default:
			neg++
		}
	}
	return pos, neg
}

// significant reports whether e is significant at alpha in the direction of
// sign (+1 improvement, -1 regression). A missing effect or p-value is never
// significant.
func significant(e *schemas.Effect, alpha float64, sign float64) bool {
	if e == nil || e.PValue == nil || e.NTasks == 0 {
		return false
	}
	return *e.PValue < alpha && sign*e.DBar > 0
}

// observedValues splits values into the observed finite numbers and the
// count of unknowns. Not-applicable values are neither.
func observedValues(vals []schemas.Value) (obs []float64, unknown int) {
	for _, v := range vals {
		if x, ok := v.Float(); ok && finite(x) {
			obs = append(obs, x)
			continue
		}
		if v.State != schemas.ValueNotApplicable {
			unknown++
		}
	}
	return obs, unknown
}

// finite reports whether x is neither NaN nor infinite; JSON cannot carry
// either.
func finite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}

// sortedKeys returns the keys of m in order, so every output is deterministic.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
