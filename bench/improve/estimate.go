package improve

import (
	"fmt"
	"math"
	"slices"

	"github.com/maximhq/bifrost/bench/stats"
)

// Assumed values of the pre-run estimate when nothing better is known (A2.8).
const (
	assumedP0         = 0.5
	assumedRho        = 0.5
	assumedPerRunUSD  = 0.05
	improverSpendPart = 0.6 // an improver run spends this share of its cap
	estimatePower     = 0.80
)

// The sentences of Estimate.Assumed.
const (
	assumedPassRate  = "pass rate 50%"
	assumedBetween   = "half of the variation is between tasks"
	assumedSameGain  = "the gain is the same on every task"
	assumedImprover  = "an improver run spends 60% of its cap"
	assumedRunCost   = "a run costs $0.05"
	historyTaskFloor = 8
)

// AssumedNoiseText is the warning when the organization's own run history
// gives no noise figure for this agent on this benchmark.
const AssumedNoiseText = "This estimate assumes typical run-to-run noise because this agent has no past runs on this benchmark here; the real smallest detectable gain can differ."

// formulaEstimator is the pre-run estimator of A2.8. It is pure: the route
// fills its inputs.
type formulaEstimator struct{}

func init() { DefaultEstimator = formulaEstimator{} }

// mdeFormula is MDE(n, m, a) of A2.8 as a fraction: the smallest mean gain
// a one-sided test at level a detects with 80% power on n tasks with m
// trials per arm, for within-task variance w and no gain variation between
// tasks. Fewer than 2 tasks give +Inf.
func mdeFormula(n, m int, a, w float64) float64 {
	if n < 2 || m < 1 || a <= 0 || a >= 1 {
		return math.Inf(1)
	}
	sd := math.Sqrt(2 * w / float64(m))
	return (stats.TQuantile(1-a, n-1) + stats.TQuantile(estimatePower, n-1)) * sd / math.Sqrt(float64(n))
}

// screenRuns is screen(K) of A2.8: the practice runs of one round's
// Sequential Halving on nd tasks with ts trials. The chunks are the halving
// levels of A2.3 (floor(nd/L), the first nd mod L one larger).
func screenRuns(k, nd, ts int) int {
	if k <= 1 {
		return nd * ts
	}
	levels := int(math.Ceil(math.Log2(float64(k))))
	total, s := 0, k
	for j := range levels {
		chunk := nd / levels
		if j < nd%levels {
			chunk++
		}
		total += s * chunk * ts
		s = (s + 1) / 2
	}
	return total
}

// Estimate computes the run counts, money, time, detectable gains,
// refusals and warnings of A2.8.
func (formulaEstimator) Estimate(in EstimateInput) Estimate {
	s := in.Spec
	if s == nil {
		s = &Spec{}
		s.Defaults()
	}
	nd, na, ns, h := in.Splits.Practice, in.Splits.Gate, in.Splits.HeldOut, in.Splits.Impossible
	ts, tc, tz := s.Screen.Trials, s.Commit.Trials, s.Sealed.Trials
	r, k := max(1, s.Rounds), max(1, s.Improver.Candidates)
	looks := s.Commit.AuditLooks
	if looks < 1 {
		looks = r
	}
	curve := s.Sealed.Curve
	nullOn := slices.Contains(s.Controls, ControlNullImprover)
	retriesOn := slices.Contains(s.Controls, ControlRetries)
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}

	out := Estimate{OK: true, Pool: in.Pool, Splits: in.Splits, Assumed: []string{}, Refusals: []EstimateNote{}, Warnings: []EstimateNote{}}

	// Runs. The merge slot is code (no improver run) from round 2 on.
	screen := screenRuns(k, nd, ts)
	gate := 2 * (na + h) * tc
	curveRuns := b2i(curve) * ns * tz
	improverLoop := 0
	for round := 1; round <= r; round++ {
		m := 0
		if s.Improver.Merge && k >= 2 && round >= 2 {
			m = 1
		}
		improverLoop += k - m
	}
	out.Runs.Baseline = nd*ts + curveRuns
	out.Runs.Practice = r * screen
	out.Runs.Gate = r * gate
	out.Runs.HeldOutCurve = curveRuns + r*curveRuns
	out.Runs.Final = (2 + b2i(nullOn)) * ns * tz
	out.Runs.Improver = improverLoop + b2i(nullOn)*r
	out.Runs.Total = out.Runs.Baseline + r*(screen+gate+curveRuns) + out.Runs.Final
	// A study (A4.1) runs L whole loops, each without its own final check
	// and controls, then one held-out experiment with version 0 and each
	// loop's final.
	loops := max(1, s.Replicate.Loops)
	if loops >= 2 {
		loopRuns := out.Runs.Baseline + r*(screen+gate+curveRuns)
		out.Runs.Baseline *= loops
		out.Runs.Practice *= loops
		out.Runs.Gate *= loops
		out.Runs.HeldOutCurve *= loops
		out.Runs.Improver = loops * improverLoop
		out.Runs.Final = (loops + 1) * ns * tz
		out.Runs.Total = loops*loopRuns + out.Runs.Final
		retriesOn = false
		out.Loops = loops
	}

	// Money.
	c, src := in.PerRunUSD, in.RunSource
	if c <= 0 {
		c, src = assumedPerRunUSD, "assumed"
	}
	if src == "" {
		src = "forecast"
	}
	money := func(capShare float64) float64 {
		iRun := capShare * s.Improver.BudgetUSD
		total := float64(out.Runs.Total)*c + float64(out.Runs.Improver)*iRun
		if retriesOn {
			total += float64(r*(screen+gate))*c + float64(improverLoop)*iRun
		}
		return total
	}
	expected := money(improverSpendPart)
	out.USD = EstimateUSD{Low: round2(0.7 * expected), Expected: round2(expected), High: round2(1.5 * money(1)),
		Limit: s.Budget.TotalUSD, PerRun: c, PerRunSource: src}
	if loops >= 2 && s.Budget.TotalUSD > 0 {
		// The study's hard stops keep it under its total, so the high figure
		// is at most the total.
		out.USD.High = math.Min(out.USD.High, s.Budget.TotalUSD)
		out.USD.LoopLimit = LoopShareUSD(s.Budget.TotalUSD, loops)
		out.USD.LimitText = StudyLimitText(s.Budget.TotalUSD, loops)
	}
	if in.PerRunMin > 0 {
		mins := in.PerRunMin * float64(out.Runs.Total)
		out.Minutes = EstimateMinutes{Low: math.Round(0.7 * mins), High: math.Round(1.5 * mins)}
	}

	// Detectable gains.
	p0, rho := in.P0, in.Rho
	w := in.W
	if w <= 0 {
		if p0 <= 0 || p0 >= 1 {
			p0 = assumedP0
			out.Assumed = append(out.Assumed, assumedPassRate)
		}
		if rho <= 0 || rho >= 1 {
			rho = assumedRho
			out.Assumed = append(out.Assumed, assumedBetween)
		}
		w = p0 * (1 - p0) * (1 - rho)
	}
	out.Assumed = append(out.Assumed, assumedSameGain, assumedImprover)
	if src == "assumed" {
		out.Assumed = append(out.Assumed, assumedRunCost)
	}
	gateLevel := s.Commit.Alpha / float64(looks)
	aMain := mainLevel(s)
	gateMDE := 100 * mdeFormula(na, tc, gateLevel, w)
	claimMDE := 100 * mdeFormula(ns, tz, aMain, w)
	if !math.IsInf(gateMDE, 0) && !math.IsNaN(gateMDE) {
		out.Detectable.GatePoints = math.Round(gateMDE*10) / 10
	}
	if !math.IsInf(claimMDE, 0) && !math.IsNaN(claimMDE) {
		out.Detectable.HeldOutPoints = math.Round(claimMDE*10) / 10
	}

	// Refusals.
	refuse := func(code, msg string) {
		out.OK = false
		out.Refusals = append(out.Refusals, EstimateNote{Code: code, Message: msg})
	}
	trial := s.IsTrial()
	if trial {
		// A trial is sized to show the loop works, not to decide: the gate
		// and held-out limits below would refuse it, so they become the
		// trial's own notice instead. It still needs practice tasks.
		out.Trial = true
		out.Warnings = append(out.Warnings, EstimateNote{Code: "trial", Message: TrialText})
	}
	if !trial && stats.MinPossiblePUpper(ns) > aMain {
		refuse("too_few_held_out", fmt.Sprintf("This benchmark gives %d held-out tasks. With so few, no result could ever be shown to be better. Use a benchmark with at least 40 tasks, or add tasks.", ns))
	}
	if !trial && stats.MinPossiblePUpper(na) > gateLevel {
		refuse("gate_never_passes", fmt.Sprintf("With %d gate tasks no new version could ever be kept. Use at least 40 tasks, or fewer rounds.", na))
	}
	if nd < 4 {
		refuse("too_few_practice", fmt.Sprintf("The improver needs at least 4 practice tasks to learn from; this benchmark gives %d.", nd))
	}
	if loops >= 2 && s.Budget.TotalUSD > 0 {
		if share := out.USD.LoopLimit; share < s.Improver.BudgetUSD {
			refuse("loop_share_small", fmt.Sprintf("A total of %s for %d loops leaves %s for each loop, less than one improver round may spend (%s). Raise the total or use fewer loops.",
				formatUSD(s.Budget.TotalUSD), loops, formatUSD(share), formatUSD(s.Improver.BudgetUSD)))
		}
		if expected > s.Budget.TotalUSD {
			refuse("over_limit", fmt.Sprintf("The expected cost of %s is above the study's total of %s. Raise the total, use fewer loops or pick a smaller preset.",
				formatUSD(expected), formatUSD(s.Budget.TotalUSD)))
		}
	} else if s.Budget.TotalUSD > 0 && out.USD.High > s.Budget.TotalUSD {
		refuse("over_limit", fmt.Sprintf("The estimate (%s to %s) is above your spending limit of %s. Raise the limit or pick a smaller preset.",
			formatUSD(out.USD.Low), formatUSD(out.USD.High), formatUSD(s.Budget.TotalUSD)))
	}

	// Warnings.
	warn := func(code, msg string) { out.Warnings = append(out.Warnings, EstimateNote{Code: code, Message: msg}) }
	if !trial && claimMDE > 15 && !math.IsInf(claimMDE, 0) {
		warn("weak_held_out", fmt.Sprintf("With %d held-out tasks only a gain of about %.0f points or more can be confirmed. A smaller real gain will end as not proven yet.", ns, claimMDE))
	}
	if !trial && gateMDE > 20 && !math.IsInf(gateMDE, 0) {
		warn("weak_gate", fmt.Sprintf("The gate tasks are few: a new version is kept only when it is about %.0f points better on them.", gateMDE))
	}
	if in.W <= 0 {
		warn("assumed_noise", AssumedNoiseText)
	}
	return out
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

// formatUSD writes dollars with two decimals.
func formatUSD(x float64) string { return fmt.Sprintf("$%.2f", x) }

// HistoryNoise turns earlier results of the incumbent into the estimator's
// P0 and W: per task, the successes of its trials. Tasks with fewer than 2
// trials are left out; fewer than 8 such tasks give ok false (A2.8: then
// the estimate assumes typical noise). W is the one-way ANOVA within mean
// square, P0 the pooled mean.
func HistoryNoise(byTask map[string][]float64) (p0, w float64, runs int, ok bool) {
	var ss, sum float64
	df, n := 0, 0
	for _, ys := range byTask {
		if len(ys) < 2 {
			continue
		}
		n++
		m := 0.0
		for _, y := range ys {
			m += y
		}
		m /= float64(len(ys))
		for _, y := range ys {
			ss += (y - m) * (y - m)
			sum += y
		}
		runs += len(ys)
		df += len(ys) - 1
	}
	if n < historyTaskFloor || df == 0 {
		return 0, 0, runs, false
	}
	return sum / float64(runs), ss / float64(df), runs, true
}
