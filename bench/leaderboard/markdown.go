package leaderboard

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// shortHash is how many hex characters of a config hash the tables show.
const shortHash = 12

// Markdown renders a board as a Markdown report: the ranking table with the
// tier column, a table of per-run aggregates, resilience on a clean board,
// a footnote for the runs left out and one for the grade source of each
// experiment. A synthetic board opens with a banner saying its numbers are
// not findings. It is pure.
func Markdown(lb *schemas.Leaderboard) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Leaderboard: %s, condition %s\n\n", orDash(lb.Suite), lb.Condition)
	if lb.Synthetic == SyntheticOnly {
		b.WriteString("> **SYNTHETIC.** Scripted agent or scripted model runs only. These numbers exercise the pipeline; they are not findings.\n\n")
	}
	if lb.Split == schemas.SplitHeldOut { // Suite health area (split.go)
		b.WriteString("> **HELD-OUT BOARD.** Runs of held-out tasks only. Kept apart from the public board; never compare the two directly.\n\n")
	}
	switch lb.Grading {
	case GradingJudged, GradingMixed:
		fmt.Fprintf(&b, "> **JUDGED.** A judge model decided whether answers are correct (%s); these results are a model's opinion, not a deterministic check (docs/answer-grading.md).\n\n",
			orNone(strings.Join(lb.Judges, ", ")))
	case GradingUnknown:
		if len(lb.Entries) > 0 {
			b.WriteString("> **GRADING UNKNOWN.** No graded run carries grade evidence; whether a judge model decided these results is not recorded.\n\n")
		}
	}
	scope := "each entry over its own tasks"
	if lb.Scope == ScopeCommon {
		scope = "every entry over the common tasks only"
	}
	fmt.Fprintf(&b, "Ranked by %s (mean over tasks of the per-task rate); there is no blended score. "+
		"Tier 1: cannot be told apart from the leader at alpha %s (exact sign-flip test on the tasks both ran, Holm-adjusted). "+
		"Tier 2: significantly behind the leader. Rank alone is not a claim that one agent beats another.\n\n",
		lb.SortMetric, strconv.FormatFloat(lb.Alpha, 'f', -1, 64))
	fmt.Fprintf(&b, "Scope %s: %s. Common tasks (%d): %s.\n\n", lb.Scope, scope, len(lb.CommonTasks), orNone(strings.Join(lb.CommonTasks, ", ")))
	fmt.Fprintf(&b, "Reliability is pass^k, the chance that k tries of a task all succeed (unbiased, mean over tasks), with k shown per entry: "+
		"min(%d, the fewest trials per task among its experiments that repeat tasks) unless the query sets k.\n\n", MaxDefaultK)
	if len(lb.Entries) == 0 {
		b.WriteString("No graded runs for this suite and condition.\n\n")
	} else {
		writeRanking(&b, lb.Entries)
		writeMetrics(&b, lb.Entries)
		writeResilience(&b, lb.Entries)
	}
	writeExcluded(&b, lb.Excluded)
	writeGradeSources(&b, lb)
	fmt.Fprintf(&b, "Generated %s.\n", lb.GeneratedAt.UTC().Format(time.RFC3339))
	return b.String()
}

// writeRanking writes the main table.
func writeRanking(b *strings.Builder, entries []schemas.LeaderboardEntry) {
	b.WriteString("| Rank | Tier | Agent | Label | Model | Config | Success [95% interval] | Tasks solved | Reliability | Tasks | Runs | Cost per success | p vs leader | Tier reason |\n")
	b.WriteString("|---:|---:|---|---|---|---|---|---:|---|---:|---:|---:|---:|---|\n")
	for _, e := range entries {
		p := "-"
		if e.PVsLeader != nil {
			p = formatP(*e.PVsLeader)
		}
		reliability := percent(e.Reliability)
		switch _, known := e.Reliability.Float(); {
		case !known && e.Trials < 2:
			// fix wave 3, N295: one try per task measures no reliability.
			reliability = fmt.Sprintf("not measured (needs 2 or more tries per task; this ran %d)", e.Trials)
		case e.Trials >= 2:
			reliability += fmt.Sprintf(" (pass^%d)", e.Trials)
		}
		fmt.Fprintf(b, "| %d | %d | %s | %s | %s | `%s` | %s | %d/%d | %s | %d | %d | %s | %s | %s |\n",
			e.Rank, e.Tier, cell(e.Agent), cell(e.Label), cell(e.Model), short(e.ConfigHash), success(e.Success),
			e.TasksSolved, e.Tasks, reliability, e.Tasks, e.Runs, usd(e.Metrics[MetricCostPerSuccess]), p, cell(e.TierReason))
	}
	b.WriteString("\n")
}

// writeMetrics writes the per-run aggregates.
func writeMetrics(b *strings.Builder, entries []schemas.LeaderboardEntry) {
	b.WriteString("Per-run medians (steps, tool calls, tokens, cost, wall time) and shares of runs (false completion, safe success). " +
		"False completion is the share of runs that claimed success while the hidden verifier failed, over runs whose outcome and claim are known; " +
		"a run whose verifier passed counts as 0.\n\n")
	b.WriteString("| Rank | Config | Steps | Tool calls | Tokens | Cost | Wall time s | False completion (share of runs) | Safe success |\n")
	b.WriteString("|---:|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, e := range entries {
		m := e.Metrics
		fmt.Fprintf(b, "| %d | `%s` | %s | %s | %s | %s | %s | %s | %s |\n", e.Rank, short(e.ConfigHash),
			withReason(number(m[MetricSteps], 1), m[MetricSteps]), withReason(number(m[MetricToolCalls], 1), m[MetricToolCalls]),
			withReason(number(m[MetricTokens], 0), m[MetricTokens]), withReason(usd(m[MetricCostUSD]), m[MetricCostUSD]),
			withReason(number(m[MetricWallTime], 1), m[MetricWallTime]),
			withReason(percent(m[MetricFalseCompletion]), m[MetricFalseCompletion]), withReason(percent(m[MetricSafeSuccess]), m[MetricSafeSuccess]))
	}
	b.WriteString("\n")
}

// writeResilience writes, on a clean board, each fault condition's success
// minus clean, in percentage points.
func writeResilience(b *strings.Builder, entries []schemas.LeaderboardEntry) {
	var conds []string
	for _, e := range entries {
		for c := range e.Resilience {
			if !slices.Contains(conds, c) {
				conds = append(conds, c)
			}
		}
	}
	if len(conds) == 0 {
		return
	}
	slices.Sort(conds)
	b.WriteString("Resilience: success under each condition minus success clean, mean over the tasks graded under both, in percentage points:\n\n")
	b.WriteString("| Rank | Config | " + strings.Join(conds, " | ") + " |\n")
	b.WriteString("|---:|---|" + strings.Repeat("---:|", len(conds)) + "\n")
	for _, e := range entries {
		fmt.Fprintf(b, "| %d | `%s` |", e.Rank, short(e.ConfigHash))
		for _, c := range conds {
			v, ok := e.Resilience[c]
			s := "-"
			if ok {
				s = points(v)
			}
			b.WriteString(" " + s + " |")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// writeExcluded writes the footnote on runs left out.
func writeExcluded(b *strings.Builder, excluded []schemas.LeaderboardExclusion) {
	if len(excluded) == 0 {
		b.WriteString("Excluded runs: none.\n\n")
		return
	}
	b.WriteString("Excluded runs:\n\n")
	for _, x := range excluded {
		fmt.Fprintf(b, "- %s: %d run%s (%s)\n", x.Reason, x.Runs, plural(x.Runs), strings.Join(x.Experiments, ", "))
	}
	b.WriteString("\n")
}

// writeGradeSources writes the footnote on where each experiment's grades
// came from: one line under original, one line per experiment under latest.
func writeGradeSources(b *strings.Builder, lb *schemas.Leaderboard) {
	if lb.GradeSource == GradeSourceReviewed {
		writeReviewedSources(b, lb) // human review (review.go)
		return
	}
	exps := make([]string, 0, len(lb.GradeSources))
	for _, gs := range lb.GradeSources {
		exps = append(exps, gs.Experiment)
	}
	if lb.GradeSource != GradeSourceLatest {
		fmt.Fprintf(b, "Grade source original: every run's outcome and metrics as sealed (%s).\n\n", orNone(strings.Join(exps, ", ")))
		return
	}
	b.WriteString("Grade source latest: each experiment's newest regrade (by regrade.json created_at, ties by the larger id), read as analysis reads it; " +
		"a run the regrade has no result for keeps its original grade.\n\n")
	if len(lb.GradeSources) == 0 {
		b.WriteString("- no experiment\n\n")
		return
	}
	for _, gs := range lb.GradeSources {
		if gs.Source == GradeSourceOriginal {
			fmt.Fprintf(b, "- %s: original (no regrade), %d run%s\n", gs.Experiment, gs.Runs, plural(gs.Runs))
			continue
		}
		fmt.Fprintf(b, "- %s: %s, %d of %d run%s regraded\n", gs.Experiment, gs.Source, gs.RegradedRuns, gs.Runs, plural(gs.Runs))
	}
	b.WriteString("\n")
}

// success writes the success rate with its interval.
func success(v schemas.Value) string {
	s := percent(v)
	if v.IsObserved() && v.Bounds != nil {
		s += fmt.Sprintf(" [%.1f, %.1f]", v.Bounds[0]*100, v.Bounds[1]*100)
	}
	return s
}

// percent writes a 0..1 value as a percentage.
func percent(v schemas.Value) string {
	if x, ok := v.Float(); ok {
		return fmt.Sprintf("%.1f%%", x*100)
	}
	return state(v)
}

// points writes a difference of rates in percentage points with its reason.
func points(v schemas.Value) string {
	x, ok := v.Float()
	if !ok {
		return state(v)
	}
	s := fmt.Sprintf("%+.1f pp", x*100)
	if v.Reason != "" {
		s += " (" + v.Reason + ")"
	}
	return s
}

// number writes a value with the given decimals.
func number(v schemas.Value, decimals int) string {
	if x, ok := v.Float(); ok {
		return strconv.FormatFloat(x, 'f', decimals, 64)
	}
	return state(v)
}

// usd writes a dollar value.
func usd(v schemas.Value) string {
	if x, ok := v.Float(); ok {
		return fmt.Sprintf("$%.4f", x)
	}
	return state(v)
}

// withReason appends an observed value's reason (how many runs were
// unknown) to its text.
func withReason(s string, v schemas.Value) string {
	if v.IsObserved() && v.Reason != "" {
		return s + " (" + v.Reason + ")"
	}
	return s
}

// state writes a value that is not observed. Unknown is never written as 0.
func state(v schemas.Value) string {
	switch v.State {
	case schemas.ValueNotApplicable:
		return "n/a"
	default:
		return "unknown"
	}
}

// short returns the first characters of a config hash, without a
// "sha256:" prefix.
func short(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > shortHash {
		return h[:shortHash]
	}
	return h
}

// cell makes text safe inside a table cell.
func cell(s string) string {
	s = strings.NewReplacer("|", `\|`, "\n", " ", "\r", " ").Replace(s)
	return orDash(s)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
