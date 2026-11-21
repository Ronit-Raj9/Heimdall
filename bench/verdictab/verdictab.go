// Package verdictab turns per-task outcomes of two arms (A and B) on one
// benchmark into a plain A/B verdict for the report card, and pools several
// benchmark verdicts into one overall verdict.
//
// It reuses the experiment verdict exactly: outcomes become counted runs on
// the clean condition, verdict.ReleaseEffect computes the per-task paired
// differences and the exact sign-flip p-value, and verdict.EffectDecision
// applies the same alpha the experiment verdict uses. A winner is named only
// when that decision is directional. Otherwise the result is "tie" when there
// are enough paired tasks and the paired-t interval sits inside a small
// margin around zero, and "unclear" when the evidence is too thin to say.
package verdictab

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/verdict"
)

// Winner values.
const (
	WinnerA       = "A"
	WinnerB       = "B"
	WinnerTie     = "tie"
	WinnerUnclear = "unclear"
)

const (
	// Alpha is the significance level, the experiment verdict's default.
	Alpha = 0.05
	// TieMargin is the largest gap in success rate (0.05 = 5 points) that a
	// 95% interval may reach on either side of zero for a "tie".
	TieMargin = 0.05
	// MinTasks is the fewest paired tasks a sign-flip test can ever call
	// significant at Alpha (stats.MinPossibleP(6) = 0.03125).
	MinTasks = 6
	tieConf  = 0.95
)

// TaskOutcome is one run of one task by one arm.
type TaskOutcome struct {
	TaskID  string  `json:"task_id"`
	Trial   int     `json:"trial"`
	Success bool    `json:"success"`
	Score   float64 `json:"score"`
	// Practice marks a run the scripted practice model answered (X01). It
	// never counts: Compare drops it before anything is computed.
	Practice bool `json:"practice,omitempty"`
}

// BenchmarkVerdict is the A/B verdict on one benchmark. Delta is B's success
// rate minus A's, averaged over paired tasks (each task's trials averaged
// first). Benchmark is left for the caller to fill.
type BenchmarkVerdict struct {
	Benchmark   string    `json:"benchmark,omitempty"`
	Winner      string    `json:"winner"`
	Delta       float64   `json:"delta"`
	Significant bool      `json:"significant"`
	Summary     string    `json:"summary"`
	PValue      *float64  `json:"p_value,omitempty"`
	PValueHolm  *float64  `json:"p_value_holm,omitempty"` // PValue after Holm across the evaluation's benchmarks (set by Holm)
	CILow       *float64  `json:"ci_low,omitempty"`
	CIHigh      *float64  `json:"ci_high,omitempty"`
	Tasks       int       `json:"tasks"`      // tasks both arms ran
	Discordant  int       `json:"discordant"` // paired tasks with a nonzero difference
	RunsA       int       `json:"runs_a"`
	RunsB       int       `json:"runs_b"`
	SuccessA    float64   `json:"success_a"`            // A's success rate on paired tasks
	SuccessB    float64   `json:"success_b"`            // B's success rate on paired tasks
	ScoreDelta  float64   `json:"score_delta"`          // descriptive: mean Score B minus A on paired tasks
	TaskDiffs   []float64 `json:"task_diffs,omitempty"` // per-task B minus A, for pooling
}

// OverallVerdict pools benchmark verdicts. Wins counts benchmark winners
// under the keys A, B, tie and unclear. The overall winner comes from one
// sign-flip test over every paired task of every benchmark (a stratified
// randomization test: each task's labels flip on their own), not from the
// win count, so one large benchmark is not outvoted by several tiny ones.
type OverallVerdict struct {
	Winner      string         `json:"winner"`
	Wins        map[string]int `json:"wins"`
	Delta       float64        `json:"delta"` // pooled mean per-task difference
	Significant bool           `json:"significant"`
	PValue      *float64       `json:"p_value,omitempty"`
	Tasks       int            `json:"tasks"`
	Benchmarks  int            `json:"benchmarks"`
	Summary     string         `json:"summary"`
}

// Compare returns the A/B verdict for one benchmark from A's and B's runs.
// Only tasks both arms ran are compared, and practice runs never count.
func Compare(a, b []TaskOutcome) BenchmarkVerdict {
	a, b = realOutcomes(a), realOutcomes(b)
	out := BenchmarkVerdict{Winner: WinnerUnclear, RunsA: len(a), RunsB: len(b)}
	switch {
	case len(a) == 0 && len(b) == 0:
		out.Summary = "No runs yet, so there is nothing to compare."
		return out
	case len(a) == 0:
		out.Summary = "A has no runs, so there is nothing to compare."
		return out
	case len(b) == 0:
		out.Summary = "B has no runs, so there is nothing to compare."
		return out
	}
	runs := append(toRuns(a, verdict.ArmBaseline), toRuns(b, verdict.ArmCandidate)...)
	eff := verdict.ReleaseEffect(runs, schemas.ConditionClean, verdict.MetricVerifiedSuccess, verdict.ArmBaseline, verdict.ArmCandidate)
	if eff == nil || eff.NTasks == 0 {
		out.Summary = "A and B ran no task in common, so there is nothing to compare."
		return out
	}
	out.Tasks = eff.NTasks
	out.Discordant = eff.NNonzero
	out.Delta = eff.DBar
	out.PValue = eff.PValue
	out.TaskDiffs = make([]float64, len(eff.PerTask))
	var sa, sb float64
	for i, td := range eff.PerTask {
		out.TaskDiffs[i] = td.D
		sa += td.MeanA
		sb += td.MeanB
	}
	n := float64(len(eff.PerTask))
	out.SuccessA, out.SuccessB = sa/n, sb/n
	out.ScoreDelta = scoreDelta(a, b, eff.PerTask)
	lo, hi, ok := stats.PairedT(out.TaskDiffs, tieConf)
	if ok {
		out.CILow, out.CIHigh = &lo, &hi
	}

	switch verdict.EffectDecision(eff, Alpha) {
	case schemas.DecisionImproved:
		out.Winner, out.Significant = WinnerB, true
	case schemas.DecisionRegressed:
		out.Winner, out.Significant = WinnerA, true
	default:
		out.Winner = tieOrUnclear(out.Tasks, ok, lo, hi)
	}
	out.Summary = benchmarkSummary(out)
	return out
}

// realOutcomes drops the practice runs (TaskOutcome.Practice).
func realOutcomes(xs []TaskOutcome) []TaskOutcome {
	out := make([]TaskOutcome, 0, len(xs))
	for _, x := range xs {
		if !x.Practice {
			out = append(out, x)
		}
	}
	return out
}

// Holm adjusts the benchmark verdicts of one evaluation for testing many
// benchmarks at once: each p-value gets Holm's step-down adjustment across
// the benchmarks that have one, Significant is read from the adjusted value,
// and a winner whose adjusted p-value is no longer below Alpha becomes "tie"
// or "unclear" by the same rule Compare uses. The input is not changed. With
// one benchmark nothing changes.
func Holm(vs []BenchmarkVerdict) []BenchmarkVerdict {
	out := slices.Clone(vs)
	var idx []int
	var ps []float64
	for i, v := range out {
		if v.PValue != nil {
			idx = append(idx, i)
			ps = append(ps, *v.PValue)
		}
	}
	adj := holm(ps)
	for k, i := range idx {
		p := adj[k]
		v := &out[i]
		v.PValueHolm = &p
		if p < Alpha || !v.Significant {
			continue
		}
		v.Significant = false
		lo, hi, ok := 0.0, 0.0, v.CILow != nil && v.CIHigh != nil
		if ok {
			lo, hi = *v.CILow, *v.CIHigh
		}
		v.Winner = tieOrUnclear(v.Tasks, ok, lo, hi)
		v.Summary = benchmarkSummary(*v) + HolmNote
	}
	return out
}

// HolmNote ends the summary of a verdict that Holm's correction took back.
const HolmNote = " (not significant once corrected for testing many benchmarks)"

// holm is Holm's step-down adjustment of p, in input order, capped at 1 and
// monotone. A NaN p is taken as 1.
func holm(p []float64) []float64 {
	m := len(p)
	order := make([]int, m)
	clean := make([]float64, m)
	for i, v := range p {
		order[i], clean[i] = i, v
		if math.IsNaN(v) {
			clean[i] = 1
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(clean[a], clean[b]) })
	out := make([]float64, m)
	running := 0.0
	for rank, i := range order {
		running = math.Max(running, math.Min(1, float64(m-rank)*clean[i]))
		out[i] = running
	}
	return out
}

// Overall pools benchmark verdicts into one. Wins count the benchmark
// winners after Holm, so the tally never shows more significant wins than
// the correction allows; pass verdicts from Compare (Holm is applied here).
func Overall(vs []BenchmarkVerdict) OverallVerdict {
	vs = Holm(vs)
	out := OverallVerdict{Winner: WinnerUnclear, Wins: map[string]int{WinnerA: 0, WinnerB: 0, WinnerTie: 0, WinnerUnclear: 0}, Benchmarks: len(vs)}
	var diffs []float64
	for _, v := range vs {
		w := v.Winner
		if _, ok := out.Wins[w]; !ok {
			w = WinnerUnclear
		}
		out.Wins[w]++
		diffs = append(diffs, v.TaskDiffs...)
	}
	out.Tasks = len(diffs)
	if len(vs) == 0 {
		out.Summary = "No benchmarks yet, so there is no overall verdict."
		return out
	}
	if len(diffs) == 0 {
		out.Summary = "No benchmark has tasks both A and B ran, so there is no overall verdict."
		return out
	}
	out.Delta = stats.Mean(diffs)
	sf := stats.SignFlip(diffs)
	p := sf.P
	out.PValue = &p
	lo, hi, ok := stats.PairedT(diffs, tieConf)
	switch {
	case p < Alpha && out.Delta > 0:
		out.Winner, out.Significant = WinnerB, true
	case p < Alpha && out.Delta < 0:
		out.Winner, out.Significant = WinnerA, true
	default:
		out.Winner = tieOrUnclear(len(diffs), ok, lo, hi)
	}
	out.Summary = overallSummary(out)
	return out
}

// tieOrUnclear reads a non-significant result: a tie needs enough tasks for
// the test to have had a chance and an interval inside the margin.
func tieOrUnclear(tasks int, ciOK bool, lo, hi float64) string {
	if tasks >= MinTasks && ciOK && lo >= -TieMargin && hi <= TieMargin {
		return WinnerTie
	}
	return WinnerUnclear
}

// toRuns turns outcomes into counted clean runs of one arm with an observed
// verified_success, the shape verdict.ReleaseEffect reads.
func toRuns(xs []TaskOutcome, arm string) []schemas.SealedRun {
	out := make([]schemas.SealedRun, 0, len(xs))
	for i, x := range xs {
		y := 0.0
		if x.Success {
			y = 1
		}
		trial := x.Trial
		if trial <= 0 {
			trial = i + 1
		}
		out = append(out, schemas.SealedRun{
			Slot: schemas.Slot{
				SlotID:    arm + "/" + x.TaskID + "/" + strconv.Itoa(trial) + "/" + strconv.Itoa(i),
				Task:      x.TaskID,
				Arm:       arm,
				Condition: schemas.ConditionClean,
				Trial:     trial,
				Counted:   true,
			},
			Metrics: map[string]schemas.Value{
				verdict.MetricVerifiedSuccess: schemas.Observed(y, schemas.EvidenceOutcome),
			},
		})
	}
	return out
}

// scoreDelta is the mean over paired tasks of B's mean Score minus A's.
func scoreDelta(a, b []TaskOutcome, paired []schemas.TaskDiff) float64 {
	ma, mb := meanScores(a), meanScores(b)
	if len(paired) == 0 {
		return 0
	}
	s := 0.0
	for _, td := range paired {
		s += mb[td.Task] - ma[td.Task]
	}
	return s / float64(len(paired))
}

func meanScores(xs []TaskOutcome) map[string]float64 {
	sum, n := map[string]float64{}, map[string]int{}
	for _, x := range xs {
		if math.IsNaN(x.Score) || math.IsInf(x.Score, 0) {
			continue
		}
		sum[x.TaskID] += x.Score
		n[x.TaskID]++
	}
	out := make(map[string]float64, len(sum))
	for k, v := range sum {
		out[k] = v / float64(n[k])
	}
	return out
}

func points(d float64) string {
	p := math.Round(math.Abs(d) * 100)
	if p == 1 {
		return "1 point"
	}
	return fmt.Sprintf("%.0f points", p)
}

func benchmarkSummary(v BenchmarkVerdict) string { return BenchmarkSummary(v, WinnerA, WinnerB) }

// BenchmarkSummary is v's plain sentence with the arms called nameA and
// nameB (agent names on the report card). A verdict whose winner was already
// relabelled is read by its delta; the Holm note is not added here.
func BenchmarkSummary(v BenchmarkVerdict, nameA, nameB string) string {
	name := map[string]string{WinnerA: nameA, WinnerB: nameB}
	switch v.Winner {
	case WinnerB, WinnerA:
		win, lose := nameA, nameB
		if v.Winner == WinnerB {
			win, lose = nameB, nameA
		}
		p := 0.0
		if v.PValue != nil {
			p = *v.PValue
		}
		return fmt.Sprintf("%s solves %s more than %s over %d tasks, and the gap is real (p = %.3f).", win, points(v.Delta), lose, v.Tasks, p)
	case WinnerTie:
		return fmt.Sprintf("%s and %s do about the same over %d tasks: any gap is under %s.", nameA, nameB, v.Tasks, points(TieMargin))
	}
	if v.Tasks < MinTasks {
		return fmt.Sprintf("Only %d tasks in common; at least %d are needed before any gap can be called real.", v.Tasks, MinTasks)
	}
	if v.Delta == 0 {
		return fmt.Sprintf("No gap over %d tasks, but too few runs to call it a tie.", v.Tasks)
	}
	lead := WinnerB
	if v.Delta < 0 {
		lead = WinnerA
	}
	return fmt.Sprintf("%s is ahead by %s over %d tasks, but that could be chance. More tasks or trials would settle it.", name[lead], points(v.Delta), v.Tasks)
}

func overallSummary(o OverallVerdict) string { return OverallSummary(o, WinnerA, WinnerB) }

// OverallSummary is o's plain sentence with the arms called nameA and nameB.
// o's Winner and Wins must still use the keys A and B.
func OverallSummary(o OverallVerdict, nameA, nameB string) string {
	counts := winsText(o, nameB)
	switch o.Winner {
	case WinnerB, WinnerA:
		win := nameA
		if o.Winner == WinnerB {
			win = nameB
		}
		p := 0.0
		if o.PValue != nil {
			p = *o.PValue
		}
		return fmt.Sprintf("%s is better overall by %s per task across %d tasks (p = %.3f). %s", win, points(o.Delta), o.Tasks, p, counts)
	case WinnerTie:
		return fmt.Sprintf("%s and %s are about the same overall across %d tasks. %s", nameA, nameB, o.Tasks, counts)
	}
	if o.Tasks < MinTasks {
		return fmt.Sprintf("Too little evidence for an overall verdict: only %d tasks in common. %s", o.Tasks, counts)
	}
	return fmt.Sprintf("Unclear overall: the pooled gap of %s per task across %d tasks could be chance. %s", points(o.Delta), o.Tasks, counts)
}

// winsText reads the win counts from B's side (called nameB), as the report
// card does.
func winsText(o OverallVerdict, nameB string) string {
	s := fmt.Sprintf("%s is better on %d of %d benchmarks, worse on %d", nameB, o.Wins[WinnerB], o.Benchmarks, o.Wins[WinnerA])
	var rest []string
	if t := o.Wins[WinnerTie]; t > 0 {
		rest = append(rest, fmt.Sprintf("tied on %d", t))
	}
	if u := o.Wins[WinnerUnclear]; u > 0 {
		rest = append(rest, fmt.Sprintf("unclear on %d", u))
	}
	for _, r := range rest {
		s += ", " + r
	}
	return s + "."
}
