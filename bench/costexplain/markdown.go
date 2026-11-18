package costexplain

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Markdown renders an explanation as the CLI prints it and the docs quote
// it: the per-arm picture, the two waterfalls, the measuring costs kept
// apart, the cache findings and the notes.
func Markdown(e *Explanation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Why arm %s cost more or less than arm %s\n\n", e.B.Arm, e.A.Arm)
	fmt.Fprintf(&b, "Experiment %s, suite %s, condition %s. Price snapshot: %s.\n\n", e.ExpID, e.Suite, e.Condition, orNone(e.PriceRef))
	if e.Synthetic {
		b.WriteString("**Synthetic: scripted agent, scripted model or smoke experiment. The numbers are not findings.**\n\n")
	}
	b.WriteString("| | arm " + e.A.Arm + " | arm " + e.B.Arm + " |\n|---|---|---|\n")
	row := func(name, a, bb string) { fmt.Fprintf(&b, "| %s | %s | %s |\n", name, a, bb) }
	row("label", e.A.Label, e.B.Label)
	row("model (served)", modelText(e.A), modelText(e.B))
	row("graded runs (verified)", fmt.Sprintf("%d (%d)", e.A.Runs, e.A.Successes), fmt.Sprintf("%d (%d)", e.B.Runs, e.B.Successes))
	row("cost per run", ValueText(e.A.CostPerRun), ValueText(e.B.CostPerRun))
	row("cost per verified success", ValueText(e.A.CostPerSuccess), ValueText(e.B.CostPerSuccess))
	row("cache-neutral cost per run", ValueText(e.A.CacheNeutralPerRun), ValueText(e.B.CacheNeutralPerRun))
	row("cache-neutral cost per success", ValueText(e.A.CacheNeutralPerSuccess), ValueText(e.B.CacheNeutralPerSuccess))
	row("cost of failed runs, per run", ValueText(e.A.FailedRunCostPerRun), ValueText(e.B.FailedRunCostPerRun))
	row("cache read share", shareText(e.A.CacheReadShare), shareText(e.B.CacheReadShare))
	row("cache write share", shareText(e.A.CacheWriteShare), shareText(e.B.CacheWriteShare))
	row("model turns per run", fmt.Sprintf("%.1f", e.A.TurnsPerRun), fmt.Sprintf("%.1f", e.B.TurnsPerRun))
	row("input / output tokens per turn", fmt.Sprintf("%.0f / %.0f", e.A.InputTokensPerTurn, e.A.OutputTokensPerTurn), fmt.Sprintf("%.0f / %.0f", e.B.InputTokensPerTurn, e.B.OutputTokensPerTurn))
	row("attempts / wasted per run", fmt.Sprintf("%.1f / %.1f", e.A.AttemptsPerRun, e.A.WastedAttemptsPerRun), fmt.Sprintf("%.1f / %.1f", e.B.AttemptsPerRun, e.B.WastedAttemptsPerRun))
	row("retries / fallbacks / rejected per run", fmt.Sprintf("%.2f / %.2f / %.2f", e.A.RetriesPerRun, e.A.FallbacksPerRun, e.A.RejectedPerRun), fmt.Sprintf("%.2f / %.2f / %.2f", e.B.RetriesPerRun, e.B.FallbacksPerRun, e.B.RejectedPerRun))
	row("unpriced / unsplit rows", fmt.Sprintf("%d / %d", e.A.UnpricedRows, e.A.UnattributedRows), fmt.Sprintf("%d / %d", e.B.UnpricedRows, e.B.UnattributedRows))
	b.WriteString("\n")
	writeWaterfall(&b, "Cost per run, B minus A", e.PerRun)
	writeWaterfall(&b, "Cost per verified success, B minus A", e.PerSuccess)
	b.WriteString("## Measuring costs, kept apart\n\n")
	for _, a := range e.Apart {
		fmt.Fprintf(&b, "- %s: A %s, B %s, difference %s\n", a.Label, ValueText(a.A), ValueText(a.B), ValueText(a.Diff))
	}
	b.WriteString("\n## Cache\n\n")
	if len(e.Cache) == 0 {
		b.WriteString("No silent cache break: the cache read share did not drop by 10 points or more between the arms or over time.\n")
	}
	for _, f := range e.Cache {
		fmt.Fprintf(&b, "- **Cache read share of arm %s dropped** (%s): %s against %s. Likely cause: %s.\n", f.Arm, f.Kind, pct(f.ShareAfter), pct(f.ShareBefore)+" in "+f.Baseline, f.Cause)
		for _, ev := range f.Evidence {
			fmt.Fprintf(&b, "  - %s\n", ev)
		}
	}
	if len(e.Notes) > 0 {
		b.WriteString("\n## Notes\n\n")
		for _, n := range e.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}
	return b.String()
}

func writeWaterfall(b *strings.Builder, title string, w Waterfall) {
	fmt.Fprintf(b, "## %s\n\n", title)
	if w.Reason != "" {
		fmt.Fprintf(b, "Not applicable: %s.\n\n", w.Reason)
		return
	}
	fmt.Fprintf(b, "A %s, B %s, difference %s.\n\n| part | USD | what moved |\n|---|---|---|\n", ValueText(w.From), ValueText(w.To), ValueText(w.Total))
	for _, p := range w.Parts {
		fmt.Fprintf(b, "| %s | %s | %s |\n", p.Label, signedText(p.USD), p.Detail)
	}
	fmt.Fprintf(b, "| **total** | %s | sum of the parts |\n\n", signedText(w.Total))
}

// ValueText writes a cost value: the amount, a range for an unknown with
// bounds, else its state and reason.
func ValueText(v schemas.Value) string {
	if x, ok := v.Float(); ok {
		return money(x)
	}
	switch {
	case v.State == schemas.ValueNotApplicable:
		if v.Reason != "" {
			return "n/a (" + v.Reason + ")"
		}
		return "n/a"
	case v.Bounds != nil:
		return fmt.Sprintf("unknown, between %s and %s", money(v.Bounds[0]), money(v.Bounds[1]))
	case v.Reason != "":
		return "unknown (" + v.Reason + ")"
	default:
		return "unknown"
	}
}

func signedText(v schemas.Value) string {
	if x, ok := v.Float(); ok && x > 0 {
		return "+" + money(x)
	}
	return ValueText(v)
}

func shareText(v schemas.Value) string {
	if x, ok := v.Float(); ok {
		return pct(x)
	}
	return "unknown"
}

func modelText(a ArmCost) string {
	served := strings.Join(a.ServedModels, ", ")
	if served == "" || served == a.Model {
		return a.Model
	}
	return a.Model + " (" + served + ")"
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
