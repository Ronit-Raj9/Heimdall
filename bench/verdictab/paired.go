package verdictab

import (
	"math"

	"github.com/maximhq/bifrost/bench/stats"
)

// Gate statistics shared by the release gate and the CI gate.
const (
	// PlanningSD is the per-task standard deviation the smallest detectable
	// change assumes, the one the evaluation estimate uses
	// (handlers.EvalPowerSD).
	PlanningSD = 0.30
	// Power is the chance of detecting a change of MDE size.
	Power = 0.8
	// BootstrapResamples is how many task resamples the paired interval uses.
	BootstrapResamples = 2000
)

// PairedChange is the change from a baseline to a candidate over paired
// tasks: the mean per-task difference (candidate minus baseline) and its
// 95% paired bootstrap interval. OK is false with fewer than two tasks.
type PairedChange struct {
	Tasks  int
	Delta  float64
	Lo, Hi float64
	OK     bool
}

// Paired is the paired change of per-task differences d. The interval is a
// percentile bootstrap over tasks with a seed fixed by the input size, so
// the same differences always give the same interval.
func Paired(d []float64) PairedChange {
	pc := PairedChange{Tasks: len(d)}
	if len(d) == 0 {
		return pc
	}
	pc.Delta = stats.Mean(d)
	cs := make([]stats.Cluster, len(d))
	for i, x := range d {
		cs[i] = stats.Cluster{Sum: x, N: 1}
	}
	pc.Lo, pc.Hi, pc.OK = stats.ClusterBootstrapCI(cs, tieConf, BootstrapResamples, uint64(len(d))<<20|0x9a7e)
	return pc
}

// MDE is the smallest change n paired tasks can detect at Alpha and Power
// with PlanningSD: 0.35 (35 points) at 8 tasks, 0.20 at 20. +Inf below two.
func MDE(n int) float64 { return stats.MDE(n, PlanningSD, Alpha, Power) }

// TasksFor is the fewest paired tasks whose MDE is at most change, 0 when
// more than max would be needed.
func TasksFor(change float64, max int) int {
	return stats.TasksNeeded(math.Abs(change), PlanningSD, Alpha, Power, 2, max)
}
