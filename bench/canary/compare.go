package canary

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/stats"
)

// Test statuses.
const (
	TestWorse      = "worse"       // p at or below its Holm level, in the bad direction, past the noise floor
	TestBetter     = "better"      // the same in the good direction (never an alarm)
	TestBelowFloor = "below_floor" // significant, but smaller than the noise floor
	TestNoChange   = "no_change"   // enough changed tasks to test; no difference detected
	TestCannotTell = "cannot_tell" // too few changed tasks for p to ever reach the level
)

// MetricTest is the exact paired sign-flip test of one metric of one target
// on one condition: per task, the mean over trials now minus the mean in
// the baseline, on the tasks both observed.
type MetricTest struct {
	Key        string   `json:"key"` // arm/condition/metric; two looks flag the same key to confirm
	Arm        string   `json:"arm"`
	Label      string   `json:"label"` // the target
	Condition  string   `json:"condition"`
	Metric     string   `json:"metric"`
	HigherGood bool     `json:"higher_is_better"`
	Tasks      int      `json:"tasks"`      // tasks both sides observed
	Discordant int      `json:"discordant"` // tasks whose value changed
	Worse      int      `json:"worse"`      // tasks that moved in the bad direction
	Baseline   *float64 `json:"baseline"`   // mean over common tasks
	Current    *float64 `json:"current"`
	Delta      *float64 `json:"delta"`    // current minus baseline
	Relative   *float64 `json:"relative"` // delta over baseline; null when the baseline is 0
	P          *float64 `json:"p"`        // two-sided exact sign-flip p; null when not tested
	Level      float64  `json:"level"`    // the Holm level p was held to; 0 when not reached
	Floor      float64  `json:"floor"`    // the noise floor the change had to pass
	Status     string   `json:"status"`
	Reason     string   `json:"reason"`
}

// Comparison is one look: every test, the keys that flagged worse, and the
// per-task flips of success.
type Comparison struct {
	Alpha   float64      `json:"alpha"` // per-look family level (Holm)
	Family  int          `json:"family"`
	Tests   []MetricTest `json:"tests"`
	Flagged []string     `json:"flagged"` // keys whose status is worse
	Flips   []TaskFlip   `json:"flips"`
}

// TaskFlip is one task whose success rate changed.
type TaskFlip struct {
	Arm       string  `json:"arm"`
	Label     string  `json:"label"`
	Condition string  `json:"condition"`
	Task      string  `json:"task"`
	Baseline  float64 `json:"baseline"`
	Current   float64 `json:"current"`
}

// floorFunc returns the noise floor of a metric.
type floorFunc func(metric string) float64

// cellKey is one (arm, condition, task).
type cellKey struct{ arm, cond, task string }

// taskMeans averages a metric over trials per (arm, condition, task), over
// runs whose value is observed.
func taskMeans(runs []RunObs, metric string) map[cellKey]float64 {
	type acc struct {
		sum float64
		n   int
	}
	sums := map[cellKey]*acc{}
	for _, r := range runs {
		v := r.Value(metric)
		if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
			continue
		}
		k := cellKey{r.Arm, r.Condition, r.Task}
		a := sums[k]
		if a == nil {
			a = &acc{}
			sums[k] = a
		}
		a.sum += *v
		a.n++
	}
	out := make(map[cellKey]float64, len(sums))
	for k, a := range sums {
		out[k] = a.sum / float64(a.n)
	}
	return out
}

// Compare tests the current runs against the baseline runs, per target,
// watched condition and metric, and applies Holm's step-down over the whole
// family at alpha. A test flags worse only when it is rejected by Holm, its
// change points the bad way and its size passes the metric's noise floor.
func Compare(base, cur []RunObs, labels map[string]string, metrics []string, alpha float64, floor floorFunc) Comparison {
	c := Comparison{Alpha: alpha, Tests: []MetricTest{}, Flagged: []string{}, Flips: []TaskFlip{}}
	for _, metric := range metrics {
		bm, cm := taskMeans(base, metric), taskMeans(cur, metric)
		groups := map[[2]string][]string{}
		for k := range cm {
			if _, ok := bm[k]; ok {
				g := [2]string{k.arm, k.cond}
				groups[g] = append(groups[g], k.task)
			}
		}
		keys := make([][2]string, 0, len(groups))
		for g := range groups {
			keys = append(keys, g)
		}
		slices.SortFunc(keys, func(a, b [2]string) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
		for _, g := range keys {
			tasks := groups[g]
			slices.Sort(tasks)
			b, n := make([]float64, len(tasks)), make([]float64, len(tasks))
			for i, t := range tasks {
				b[i], n[i] = bm[cellKey{g[0], g[1], t}], cm[cellKey{g[0], g[1], t}]
				if metric == MetricSuccess && b[i] != n[i] {
					c.Flips = append(c.Flips, TaskFlip{Arm: g[0], Label: labels[g[0]], Condition: g[1], Task: t, Baseline: b[i], Current: n[i]})
				}
			}
			t := pairedTest(b, n, HigherIsBetter(metric))
			t.Key, t.Arm, t.Label, t.Condition, t.Metric = g[0]+"/"+g[1]+"/"+metric, g[0], labels[g[0]], g[1], metric
			t.Floor = floor(metric)
			c.Tests = append(c.Tests, t)
		}
	}
	// Two families, each held to alpha by Holm: success (the primary
	// signal, so its power is not split with the secondary metrics), and
	// cost, tokens and latency together. The confirmation rule then holds
	// the alarm rate down (docs/drift-canary.md).
	primary := func(t MetricTest) bool { return t.Metric == MetricSuccess }
	c.Family = holm(c.Tests, alpha, primary) + holm(c.Tests, alpha, func(t MetricTest) bool { return !primary(t) })
	for _, t := range c.Tests {
		if t.Status == TestWorse {
			c.Flagged = append(c.Flagged, t.Key)
		}
	}
	return c
}

// pairedTest runs the sign-flip test on per-task baseline and current
// values. Status is set by holm afterwards, except cannot_tell and
// no_change, which no level can turn into a flag.
func pairedTest(base, cur []float64, higherGood bool) MetricTest {
	t := MetricTest{Tasks: len(base), HigherGood: higherGood}
	if len(base) == 0 {
		t.Status, t.Reason = TestCannotTell, "no task observed on both sides"
		return t
	}
	d := make([]float64, len(base))
	for i := range base {
		d[i] = cur[i] - base[i]
		if (d[i] < 0 && higherGood) || (d[i] > 0 && !higherGood) {
			t.Worse++
		}
	}
	mb, mc := stats.Mean(base), stats.Mean(cur)
	delta := mc - mb
	t.Baseline, t.Current, t.Delta = &mb, &mc, &delta
	if mb != 0 {
		rel := delta / math.Abs(mb)
		t.Relative = &rel
	}
	res := stats.SignFlip(d)
	t.Discordant = res.NNonzero
	p := res.P
	t.P = &p
	switch {
	case res.NNonzero == 0:
		t.Status, t.Reason = TestNoChange, fmt.Sprintf("no task changed (%d tasks)", len(base))
	default:
		t.Status = "" // decided by holm
	}
	return t
}

// holm applies Holm's step-down at alpha over the family's tests still
// undecided and returns the family size (tests with no changed task count
// in it with p 1). A test whose smallest possible p (all discordant tasks
// one way) cannot reach its level is cannot_tell, never no_change.
func holm(tests []MetricTest, alpha float64, member func(MetricTest) bool) int {
	var idx []int
	m := 0
	for i, t := range tests {
		if !member(t) {
			continue
		}
		if t.Status == "" {
			idx = append(idx, i)
			m++
		} else if t.Status == TestNoChange {
			m++
		}
	}
	slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(*tests[a].P, *tests[b].P) })
	stopped := false
	for rank, i := range idx {
		t := &tests[i]
		level := alpha / float64(m-rank)
		if stats.MinPossibleP(t.Discordant) > level {
			t.Status = TestCannotTell
			t.Reason = fmt.Sprintf("only %d of %d tasks changed; even all one way could not reach p <= %.3g", t.Discordant, t.Tasks, level)
			stopped = true
			continue
		}
		if stopped || *t.P > level {
			stopped = true
			t.Status = TestNoChange
			t.Reason = fmt.Sprintf("p %.3g on %d tasks (%d changed): no difference detected at level %.3g", *t.P, t.Tasks, t.Discordant, level)
			continue
		}
		t.Level = level
		worse := (*t.Delta < 0) == t.HigherGood
		size := math.Abs(*t.Delta)
		if !HigherIsBetter(t.Metric) && t.Relative != nil {
			size = math.Abs(*t.Relative)
		}
		switch {
		case size < t.Floor:
			t.Status = TestBelowFloor
			t.Reason = fmt.Sprintf("p %.3g on %d tasks, but the change %s is under the noise floor %s", *t.P, t.Tasks, changeText(*t), floorText(t.Metric, t.Floor))
		case worse:
			t.Status = TestWorse
			t.Reason = fmt.Sprintf("p %.3g <= %.3g on %d tasks (%d of %d changed tasks worse): %s", *t.P, level, t.Tasks, t.Worse, t.Discordant, changeText(*t))
		default:
			t.Status = TestBetter
			t.Reason = fmt.Sprintf("p %.3g <= %.3g on %d tasks: %s", *t.P, level, t.Tasks, changeText(*t))
		}
	}
	return m
}

// changeText says a test's change in plain words.
func changeText(t MetricTest) string {
	if t.Delta == nil {
		return "no change measured"
	}
	switch t.Metric {
	case MetricSuccess:
		return fmt.Sprintf("success %+.1f points (%.0f%% to %.0f%%)", 100**t.Delta, 100**t.Baseline, 100**t.Current)
	default:
		if t.Relative != nil {
			return fmt.Sprintf("%s %+.0f%% (%.4g to %.4g)", t.Metric, 100**t.Relative, *t.Baseline, *t.Current)
		}
		return fmt.Sprintf("%s %+.4g (from 0)", t.Metric, *t.Delta)
	}
}

// floorText says a floor in the metric's unit.
func floorText(metric string, f float64) string {
	if metric == MetricSuccess {
		return fmt.Sprintf("%.1f points", 100*f)
	}
	return fmt.Sprintf("%.0f%%", 100*f)
}

// FlaggedSummary is one line per flagged test.
func (c Comparison) FlaggedSummary() []string {
	var out []string
	for _, t := range c.Tests {
		if t.Status == TestWorse {
			out = append(out, fmt.Sprintf("%s on %s: %s", t.Label, t.Condition, t.Reason))
		}
	}
	return out
}

// Overlap returns the keys flagged in both a and b.
func Overlap(a, b []string) []string {
	var out []string
	for _, k := range a {
		if slices.Contains(b, k) {
			out = append(out, k)
		}
	}
	return out
}

// Invalid lists the reasons an execution cannot be compared: problems the
// store found, an egress fire, instrument gates (model fidelity excepted)
// above 5% of runs, or graded coverage under 90% of the planned slots. Any
// reason makes the look invalid (a harness problem), never a drift.
func Invalid(x *Execution) []string {
	reasons := slices.Clone(x.Problems)
	graded, egress, infra := 0, 0, 0
	gates := map[string]int{}
	for _, r := range x.Runs {
		for _, p := range r.Problems {
			reasons = append(reasons, "run "+r.RunID+": "+p)
		}
		if r.Egress {
			egress++
		}
		if r.InfraError {
			infra++
		}
		if r.Success != nil {
			graded++
		}
		for _, g := range r.InvalidGates {
			gates[g]++
		}
	}
	if egress > 0 {
		reasons = append(reasons, fmt.Sprintf("egress gate fired in %d run(s): the harness leaked", egress))
	}
	n := max(len(x.Runs), 1)
	for _, g := range sortedKeys(gates) {
		if rate := float64(gates[g]) / float64(n); rate > 0.05 {
			reasons = append(reasons, fmt.Sprintf("gate %s fired in %d of %d runs (above 5%%)", g, gates[g], len(x.Runs)))
		}
	}
	if x.Injected != "" {
		// A forced drift that did not reach the runs tests nothing: the
		// fault proxy never saw them (a shared or misrouted gateway).
		notFired := 0
		for _, r := range x.Runs {
			if r.FaultFired == nil || !*r.FaultFired {
				notFired++
			}
		}
		if notFired > 0 {
			reasons = append(reasons, fmt.Sprintf("the injected fault %s did not fire in %d of %d runs: the forced drift never reached them", x.Injected, notFired, len(x.Runs)))
		}
	}
	slots := max(x.Slots, len(x.Runs))
	if slots == 0 {
		reasons = append(reasons, "no counted runs")
	} else if cov := float64(graded) / float64(slots); cov < 0.90 {
		reasons = append(reasons, fmt.Sprintf("graded coverage %.2f (%d of %d planned runs scored; %d infrastructure errors) is below 0.90", cov, graded, slots, infra))
	}
	return reasons
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// joinOr joins xs, or returns none when empty.
func joinOr(xs []string, none string) string {
	if len(xs) == 0 {
		return none
	}
	return strings.Join(xs, ", ")
}
