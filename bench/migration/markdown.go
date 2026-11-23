package migration

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/schemas"
)

// maxFlippedListed caps the flipped tasks one pair lists in markdown; the
// JSON carries all of them.
const maxFlippedListed = 20

// Markdown renders the report as `migrate report` writes it.
func Markdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Model migration report: %s\n\n", r.ExpID)
	fmt.Fprintf(&b, "Suite %s. Old model: %s (arm %s, agent %s). Overall verdicts on condition %s, alpha %s. Release rule set: %s.\n\n",
		r.Suite, r.From.Model, r.From.Arm, r.From.Agent, r.Condition, fmtAlpha(r.Alpha), orNone(r.Policy))
	if r.Synthetic {
		b.WriteString("**Synthetic: scripted agent, scripted model or smoke experiment. The numbers are not findings.**\n\n")
	}
	b.WriteString("## Recommendation\n\n")
	for _, p := range r.Pairs {
		fmt.Fprintf(&b, "- **%s to %s: %s.** %s.\n", p.From.Model, p.To.Model+" (arm "+p.To.Arm+")", actionText(p.Recommendation.Action), p.Recommendation.Reason)
	}
	b.WriteString("\n## Models\n\n| arm | model | success [95%] | pass^k | cost per verified success | median cost per run | median tokens | median wall s | median steps | latency per turn | Pareto |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, m := range r.Models {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", m.Arm, m.Model, successText(m.Success), kText(m.Reliability, m.K),
			costexplain.ValueText(m.CostPerSuccess), costexplain.ValueText(m.CostPerRun), numText(m.Tokens, "%.0f"), numText(m.WallTimeS, "%.0f"),
			numText(m.Steps, "%.0f"), numText(m.LatencyMS, "%.0f ms"), m.Pareto)
	}
	for _, p := range r.Pairs {
		writePair(&b, p)
	}
	b.WriteString("\n## Notes\n\n")
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	return b.String()
}

func writePair(b *strings.Builder, p Pair) {
	fmt.Fprintf(b, "\n## %s to %s\n\n", p.From.Model+" (arm "+p.From.Arm+")", p.To.Model+" (arm "+p.To.Arm+")")
	fmt.Fprintf(b, "**%s.** %s.\n\n", actionText(p.Recommendation.Action), p.Recommendation.Reason)
	for _, c := range p.Recommendation.Caveats {
		fmt.Fprintf(b, "- %s\n", c)
	}
	if pr := p.Recommendation.Policy; pr != nil {
		fmt.Fprintf(b, "- release rule set %s: %s", pr.RuleSetName(), pr.Decision)
		var checks []string
		for _, c := range pr.Checks {
			checks = append(checks, c.ID+" "+string(c.Status))
		}
		fmt.Fprintf(b, " (%s)\n", strings.Join(checks, ", "))
	}
	fmt.Fprintf(b, "\nOverall: %s\n\n", verdictLine(p.Overall))
	b.WriteString("### Strata (Holm across every stratum of this pair)\n\n| stratum | tasks | better / worse | mean diff | p | p adj | result |\n|---|---|---|---|---|---|---|\n")
	for _, s := range p.Strata {
		fmt.Fprintf(b, "| %s %s | %d | %d / %d | %+.3f | %s | %s | %s |\n", s.Dimension, s.Value, s.Tasks, s.Better, s.Worse, s.MeanDiff, pText(s.P), pText(s.PAdjusted), resultText(s))
	}
	bh := p.Behaviour
	b.WriteString("\n### Behaviour\n\n")
	fmt.Fprintf(b, "- false completion: %s vs %s\n", rateText(bh.FalseCompletion.From), rateText(bh.FalseCompletion.To))
	fmt.Fprintf(b, "- rejected requests per run (4xx: malformed request or tool call): %s vs %s\n", numText(bh.Rejected.From, "%.2f"), numText(bh.Rejected.To, "%.2f"))
	fmt.Fprintf(b, "- refusals per run: %s vs %s\n", numText(bh.Refusals.From, "%.2f"), numText(bh.Refusals.To, "%.2f"))
	fmt.Fprintf(b, "- retries / fallbacks per run: %s / %s vs %s / %s\n", numText(bh.Retries.From, "%.2f"), numText(bh.Fallbacks.From, "%.2f"), numText(bh.Retries.To, "%.2f"), numText(bh.Fallbacks.To, "%.2f"))
	fmt.Fprintf(b, "- gateway latency per turn: %s vs %s\n", numText(bh.LatencyMS.From, "%.0f ms"), numText(bh.LatencyMS.To, "%.0f ms"))
	for _, g := range bh.Gates {
		fmt.Fprintf(b, "- gate %s (%s): fired in %d vs %d runs, %s\n", g.Gate, g.Effect, g.From, g.To, g.Status)
	}
	writeClusters(b, "failure cluster only the new model shows", bh.NewClusters)
	writeClusters(b, "failure cluster the new model fixed", bh.FixedClusters)
	b.WriteString("\n| condition | runs | success | false completion | retry amplification |\n|---|---|---|---|---|\n")
	for _, c := range bh.Conditions {
		fmt.Fprintf(b, "| %s | %d vs %d | %s vs %s | %s vs %s | %s vs %s |\n", c.Condition, c.FromRuns, c.ToRuns, rateText(c.FromSuccess), rateText(c.ToSuccess),
			rateText(c.FalseCompletion.From), rateText(c.FalseCompletion.To), numText(c.Retries.From, "%.2f"), numText(c.Retries.To, "%.2f"))
	}
	if len(p.Flipped) > 0 {
		b.WriteString("\n### Flipped tasks\n\n| task | condition | kind | old | new | runs |\n|---|---|---|---|---|---|\n")
		for i, f := range p.Flipped {
			if i == maxFlippedListed {
				fmt.Fprintf(b, "| and %d more | | | | | |\n", len(p.Flipped)-maxFlippedListed)
				break
			}
			var runs []string
			for _, r := range f.Runs {
				runs = append(runs, fmt.Sprintf("%s %s:%s", r.Arm, r.RunID, r.Outcome))
			}
			fmt.Fprintf(b, "| %s | %s | %s | %.0f%% | %.0f%% | %s |\n", f.Task, f.Condition, f.Kind, 100*f.FromRate, 100*f.ToRate, strings.Join(runs, ", "))
		}
	}
	if e := p.Cost; e != nil {
		fmt.Fprintf(b, "\n### Cost (%s)\n\n", e.Condition)
		fmt.Fprintf(b, "Cost per verified success %s vs %s; difference %s.\n\n", costexplain.ValueText(e.PerSuccess.From), costexplain.ValueText(e.PerSuccess.To), costexplain.ValueText(e.PerSuccess.Total))
		w := e.PerSuccess
		if w.Reason != "" {
			w = e.PerRun
			fmt.Fprintf(b, "Per success not applicable (%s); per run instead.\n\n", e.PerSuccess.Reason)
		}
		for _, part := range w.Parts {
			if x, ok := part.USD.Float(); ok && x == 0 {
				continue
			}
			fmt.Fprintf(b, "- %s: %s (%s)\n", part.Label, costexplain.ValueText(part.USD), part.Detail)
		}
		for _, f := range e.Cache {
			fmt.Fprintf(b, "- cache read share of arm %s dropped from %.1f%% to %.1f%% (%s): %s\n", f.Arm, 100*f.ShareBefore, 100*f.ShareAfter, f.Kind, f.Cause)
		}
	}
}

// maxClustersListed caps the clusters of each kind one pair lists in
// markdown; the JSON carries all of them.
const maxClustersListed = 8

func writeClusters(b *strings.Builder, what string, cs []ClusterRef) {
	for i, c := range cs {
		if i == maxClustersListed {
			fmt.Fprintf(b, "- and %d more of these clusters (report.json lists every one)\n", len(cs)-maxClustersListed)
			return
		}
		fmt.Fprintf(b, "- %s: %s (%d runs, tasks %s)\n", what, c.Title, c.Count, strings.Join(c.Tasks, ", "))
	}
}

func verdictLine(v Verdict) string {
	s := fmt.Sprintf("%s. Mean difference %+.3f (new minus old, per-task success rate).", v.Reason, v.MeanDiff)
	if v.Result == ResultCannotTell && v.TasksNeeded > 0 {
		s += fmt.Sprintf(" About %d tasks would detect a difference of this size with %d%% power.", v.TasksNeeded, int(Power*100))
	}
	return s
}

func resultText(v Verdict) string {
	if v.Result == ResultCannotTell && v.TasksNeeded > 0 {
		return fmt.Sprintf("cannot tell (about %d tasks needed)", v.TasksNeeded)
	}
	return strings.ReplaceAll(v.Result, "_", " ")
}

func actionText(a string) string {
	switch a {
	case ActionMigrate:
		return "Migrate"
	case ActionDoNotMigrate:
		return "Do not migrate"
	default:
		return "Not enough evidence"
	}
}

func pText(p *float64) string {
	if p == nil {
		return "-"
	}
	return formatP(*p)
}

func successText(v schemas.Value) string {
	x, ok := v.Float()
	if !ok {
		return "unknown"
	}
	if v.Bounds == nil {
		return fmt.Sprintf("%.1f%%", 100*x)
	}
	return fmt.Sprintf("%.1f%% [%.0f, %.0f]", 100*x, 100*v.Bounds[0], 100*v.Bounds[1])
}

func kText(v schemas.Value, k int) string {
	x, ok := v.Float()
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%.1f%% (k=%d)", 100*x, k)
}

func rateText(v schemas.Value) string {
	if x, ok := v.Float(); ok {
		return fmt.Sprintf("%.1f%%", 100*x)
	}
	return "unknown"
}

func numText(v schemas.Value, format string) string {
	if x, ok := v.Float(); ok {
		return fmt.Sprintf(format, x)
	}
	if v.State == schemas.ValueNotApplicable {
		return "n/a"
	}
	return "unknown"
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
