package main

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// W7 adaptive trials and suite tiers: the report section with the tier, the
// trials used against the maximum, the runs saved, the stopping reason and,
// on an INCONCLUSIVE verdict, what more evidence would cost.

// adaptiveSection appends the section to report; it adds nothing for a
// fixed design with no tier and no "buy more evidence" line.
func adaptiveSection(an *schemas.Analysis, report string) string {
	sec := adaptiveMarkdown(an)
	if sec == "" {
		return report
	}
	return strings.TrimRight(report, "\n") + "\n\n" + sec
}

func adaptiveMarkdown(an *schemas.Analysis) string {
	if an == nil {
		return ""
	}
	v := an.Verdict
	if v.Adaptive == nil && v.Tier == "" && v.MoreEvidence == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Trials and tier\n\n")
	if v.Tier != "" {
		fmt.Fprintf(&b, "- Tier: `%s`. Compare this experiment with one of another tier only on their common tasks.\n", v.Tier)
	}
	if a := v.Adaptive; a != nil {
		fmt.Fprintf(&b, "- Adaptive rule `%s`: %d of %d planned slots required, %d run, %d not required (runs saved). Stop: %s",
			a.Rule, a.RequiredSlots, a.MaxSlots, a.RunSlots, a.RunsSaved, a.StopReason)
		if a.StopLook > 0 {
			fmt.Fprintf(&b, " at look %d of %d", a.StopLook, a.Looks)
		}
		fmt.Fprintf(&b, ". The primary decision used alpha %.4f.\n", a.DecisionAlpha)
		for _, l := range a.LookResults {
			p := "-"
			if l.PLow != nil && l.PHigh != nil {
				p = fmt.Sprintf("%.4f / %.4f", *l.PLow, *l.PHigh)
			}
			fmt.Fprintf(&b, "  - look %d: %d tasks, alpha %.4f, p (lower / upper bound) %s, d_bar %+.1f pp: %s\n", l.Look, l.Tasks, l.Alpha, p, 100*l.DBar, l.Outcome)
		}
		if len(a.Excluded) > 0 {
			fmt.Fprintf(&b, "- Left out (slots the rule never released): %s\n", strings.Join(a.Excluded, "; "))
		}
	}
	if me := v.MoreEvidence; me != nil {
		b.WriteString("- Buy more evidence: " + sizingText(me) + ".")
		if me.Combination == schemas.ExtensionInverseNormal {
			fmt.Fprintf(&b, " A follow-up is decided together with this experiment by the pre-declared inverse-normal rule at alpha %.4f.", me.AlphaKept)
		} else {
			b.WriteString(" No combination rule was declared: a follow-up decides on its own, and pooling it with this experiment is exploratory.")
		}
		if me.Command != "" {
			fmt.Fprintf(&b, " `%s`", me.Command)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// printAdaptivePlan prints the tier and the adaptive design of a plan:
// nothing for a fixed design with no tier.
func printAdaptivePlan(m *schemas.Manifest) {
	if m.Tier != nil {
		fmt.Printf("  tier %s: %d tasks\n", m.Tier.Name, len(m.Tier.Tasks))
	}
	p := m.Adaptive
	if p == nil {
		return
	}
	fmt.Printf("  adaptive rule %s: %d slots always run, %d optional (run only when the frozen rule releases them)\n", p.Rule, p.Required, p.Optional)
	switch p.Rule {
	case schemas.AdaptiveTask:
		fmt.Printf("    min %d, max %d trials per arm on %s; more only where the arms disagree or a task's trials are mixed\n", p.Min, p.Max, strings.Join(p.Conditions, ", "))
	case schemas.AdaptiveSequential:
		for i, l := range p.Looks {
			fmt.Printf("    look %d: %d tasks at alpha %.4f\n", i+1, len(l), p.LookAlphas[i])
		}
		fmt.Printf("    futility %s\n", p.Futility)
	}
}

// sizingText is the "buy more evidence" estimate in words, shared by the
// adaptive report section and `extend`, so the two never drift (audit
// qc2-03-06). Beyond the sizing cap (Over) it names the cap instead of a
// task count, since Tasks is then zero, not an estimate.
func sizingText(me *schemas.MoreEvidence) string {
	sd := "measured"
	if me.SDAssumed {
		sd = "assumed"
	}
	if me.Over {
		return fmt.Sprintf("to detect %.1f pp you need more than %d tasks in all (SD %.2f %s); runs and cost not estimated (%s)",
			me.TargetPP, me.TasksTotal, me.SD, sd, me.CostBasis)
	}
	cost := "cost unknown"
	if me.CostUSD != nil {
		cost = fmt.Sprintf("estimated cost %.2f USD", *me.CostUSD)
	}
	return fmt.Sprintf("to detect %.1f pp you need about %d more tasks (%d in all, SD %.2f %s), %d runs, %s (%s)",
		me.TargetPP, me.Tasks, me.TasksTotal, me.SD, sd, me.Runs, cost, me.CostBasis)
}
