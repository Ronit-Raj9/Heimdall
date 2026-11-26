package gwfeature

import (
	"slices"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// MinTasks is the fewest tasks with results on both setups a paired delta
// needs before it is reported.
const MinTasks = 5

// Paired is a per-task paired comparison of two setups.
type Paired struct {
	Tasks int
	Delta Interval
	P     *float64
}

// PairedTasks compares variant against control task by task: per task the
// mean of value over each setup's runs, then the mean over tasks both setups
// have of variant minus control, with a 95% paired t range and a sign flip
// p-value.
func PairedTasks(runs []Run, control, variant string, value func(Run) (float64, bool)) Paired {
	type acc struct{ sum, n float64 }
	per := map[string]map[string]*acc{control: {}, variant: {}}
	for _, r := range runs {
		byTask, ok := per[r.Arm]
		if !ok {
			continue
		}
		v, ok := value(r)
		if !ok {
			continue
		}
		a := byTask[r.Task]
		if a == nil {
			a = &acc{}
			byTask[r.Task] = a
		}
		a.sum += v
		a.n++
	}
	var tasks []string
	for task := range per[control] {
		if per[variant][task] != nil {
			tasks = append(tasks, task)
		}
	}
	sort.Strings(tasks)
	var out Paired
	if len(tasks) == 0 {
		return out
	}
	d := make([]float64, len(tasks))
	total := 0.0
	for i, task := range tasks {
		c, v := per[control][task], per[variant][task]
		d[i] = v.sum/v.n - c.sum/c.n
		total += d[i]
	}
	out.Tasks = len(tasks)
	out.Delta.Point = total / float64(len(tasks))
	if lo, hi, ok := stats.PairedT(d, 0.95); ok {
		out.Delta.Low, out.Delta.High = &lo, &hi
	}
	p := stats.SignFlip(d).P
	out.P = &p
	return out
}

// SolveValue is 1 for a verified success, 0 for a failure, unknown when the
// run has no grade.
func SolveValue(r Run) (float64, bool) {
	if r.Success == nil {
		return 0, false
	}
	if *r.Success {
		return 1, true
	}
	return 0, true
}

// CostValue is the run's cost in USD, unknown when not recorded.
func CostValue(r Run) (float64, bool) {
	if r.CostUSD == nil {
		return 0, false
	}
	return *r.CostUSD, true
}

// LatencyValue is the median latency of the run's successful model calls.
func LatencyValue(r Run) (float64, bool) {
	var l []float64
	for _, row := range r.Rows {
		if row.Kind == "message" && row.Status == "success" {
			l = append(l, row.LatencyMS)
		}
	}
	return median(l)
}

func median(v []float64) (float64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	v = slices.Clone(v)
	slices.Sort(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2], true
	}
	return (v[n/2-1] + v[n/2]) / 2, true
}

// ArmResults is one result per arm id, in the given order.
func ArmResults(runs []Run, m *schemas.Manifest, arms []string) []ArmResult {
	out := make([]ArmResult, 0, len(arms))
	for _, arm := range arms {
		res := ArmResult{Arm: arm}
		if m != nil {
			res.Label = m.Arms[arm].Label
		}
		type acc struct{ sum, n float64 }
		tasks := map[string]*acc{}
		var cost acc
		var lat []float64
		for _, r := range runs {
			if r.Arm != arm {
				continue
			}
			res.Runs++
			a := tasks[r.Task]
			if a == nil {
				a = &acc{}
				tasks[r.Task] = a
			}
			if v, ok := SolveValue(r); ok {
				a.sum += v
				a.n++
			}
			if v, ok := CostValue(r); ok {
				cost.sum += v
				cost.n++
			}
			for _, row := range r.Rows {
				if row.Kind == "message" && row.Status == "success" {
					lat = append(lat, row.LatencyMS)
				}
			}
		}
		res.Tasks = len(tasks)
		var solve acc
		for _, a := range tasks {
			if a.n > 0 {
				solve.sum += a.sum / a.n
				solve.n++
			}
		}
		if solve.n > 0 {
			v := solve.sum / solve.n
			res.SolveRate = &v
		}
		if cost.n > 0 {
			v := cost.sum / cost.n
			res.CostPerRun = &v
		}
		if v, ok := median(lat); ok {
			res.LatencyMS = &v
		}
		out = append(out, res)
	}
	return out
}
