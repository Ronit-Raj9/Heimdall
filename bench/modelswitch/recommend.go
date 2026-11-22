package modelswitch

import (
	"fmt"
	"slices"

	"github.com/maximhq/bifrost/bench/migration"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Recommendation strengths.
const (
	StrengthStrong = "strong" // the statistics separate the choice from the alternatives
	StrengthWeak   = "weak"   // the statistics only fail to tell the models apart
)

// recommend picks the default model. The best model by measured quality
// is the reference; every model that cannot be told apart from it is
// tied; the default is the cheapest tied model by cost per verified
// success (the best itself when no tied model's cost is known). The
// answer is never stronger than the statistics: a pick among tied models
// is weak, and fewer than MinSegmentTasks tasks is not enough evidence.
func recommend(rep *Report, ev *schemas.Evaluation, benches []benchRuns) Recommendation {
	r := Recommendation{Reasons: []string{}, Caveats: []string{migration.TunedPromptNote}, Tied: []string{}}
	byLabel := map[string]Model{}
	for _, m := range rep.Models {
		byLabel[m.Label] = m
	}
	if rep.Best == "" {
		r.Action, r.Strength = ActionNotEnough, StrengthWeak
		r.Summary = "No model has a graded run yet, so there is nothing to compare. Wait for the evaluation to finish, or check why its benchmarks were skipped."
		return finish(r, ev)
	}
	best := byLabel[rep.Best]
	r.Tied = append(r.Tied, rep.Best)
	allWorse := len(rep.Comparisons) > 0
	for _, c := range rep.Comparisons {
		switch c.Result {
		case ResultCannotTell:
			r.Tied = append(r.Tied, c.Label)
			allWorse = false
		case ResultWorse:
		default:
			allWorse = false
		}
	}
	def := rep.Best
	for _, l := range r.Tied[1:] {
		c, ok := byLabel[l].CostPerSuccess.Float()
		if !ok {
			continue
		}
		cur, okCur := byLabel[def].CostPerSuccess.Float()
		if !okCur || c < cur {
			def = l
		}
	}
	r.Model = def
	chosen := byLabel[def]
	switch {
	case best.Tasks < MinSegmentTasks:
		r.Action, r.Strength = ActionNotEnough, StrengthWeak
	case def == rep.Current:
		r.Action = ActionKeep
	default:
		r.Action = ActionSwitch
	}
	if r.Strength == "" {
		r.Strength = StrengthWeak
		if def == rep.Best && allWorse {
			r.Strength = StrengthStrong
		}
	}
	cur := byLabel[rep.Current]

	// Summary.
	switch r.Action {
	case ActionNotEnough:
		r.Summary = fmt.Sprintf("Not enough evidence yet: the best model, %s, has graded runs on %d task(s). Run at least %d tasks per model before choosing.", display(best), best.Tasks, MinSegmentTasks)
	case ActionKeep:
		if r.Strength == StrengthStrong {
			r.Summary = fmt.Sprintf("Keep %s: it scored highest and every other model scored significantly lower.", display(chosen))
		} else {
			r.Summary = fmt.Sprintf("Keep %s: no other model scored significantly higher on these tasks, and none of the models that cannot be told apart from the best is cheaper per verified success.", display(chosen))
		}
	case ActionSwitch:
		switch {
		case r.Strength == StrengthStrong:
			r.Summary = fmt.Sprintf("Switch to %s: it scored highest and every other model, %s included, scored significantly lower.", display(chosen), display(cur))
		case def == rep.Best:
			r.Summary = fmt.Sprintf("Switch to %s: it scored highest. The lead over some models is within noise at this size, so this is a lean, not a proven win.", display(chosen))
		default:
			r.Summary = fmt.Sprintf("Switch to %s: it cannot be told apart from the best model, %s, on these tasks, and it is the cheapest of those per verified success.", display(chosen), display(best))
		}
	}

	// Reasons, every number from the runs.
	r.Reasons = append(r.Reasons, qualityLine(chosen))
	if def != rep.Best {
		r.Reasons = append(r.Reasons, qualityLine(best)+" (the best by measured quality)")
	}
	if rep.Current != def && rep.Current != rep.Best {
		line := qualityLine(cur) + " (the current model)"
		if c, ok := comparisonOf(rep, rep.Current); ok {
			line += "; " + c.Reason
		}
		r.Reasons = append(r.Reasons, line)
	}
	if def != rep.Current {
		r.Reasons = append(r.Reasons, costLine(chosen, cur))
		if l := latencyLine(chosen, cur); l != "" {
			r.Reasons = append(r.Reasons, l)
		}
	} else if def != rep.Best {
		r.Reasons = append(r.Reasons, costLine(chosen, best))
	}
	for _, l := range r.Tied {
		m := byLabel[l]
		if a, ok := m.Availability.Float(); ok && a < 0.99 {
			r.Caveats = append(r.Caveats, fmt.Sprintf("%s answered %.1f%% of its model calls (%d of %d failed at the provider): put it after a more available model in the fallback chain.", display(m), a*100, m.CallFailures, m.ModelCalls))
		}
	}
	if len(r.Tied) > 1 {
		names := make([]string, 0, len(r.Tied))
		for _, l := range r.Tied {
			names = append(names, display(byLabel[l]))
		}
		r.Caveats = append(r.Caveats, fmt.Sprintf("%d models cannot be told apart from the best at this size (%s). More tasks would separate them; the routing config balances traffic over them, more to the cheaper.", len(r.Tied), joinWords(names)))
	}
	benchesRun := 0
	for _, b := range benches {
		if len(b.arms) > 0 {
			benchesRun++
		}
	}
	r.Caveats = append(r.Caveats, fmt.Sprintf("Measured on %d task(s) over %d benchmark(s) with %d trial(s) each: only differences of a size these tasks can show are called significant.", best.Tasks, benchesRun, ev.Spec.Trials))
	return finish(r, ev)
}

// finish marks a report of an evaluation that is still running.
func finish(r Recommendation, ev *schemas.Evaluation) Recommendation {
	switch ev.Status {
	case schemas.EvaluationDone, schemas.EvaluationFailed, schemas.EvaluationCancelled:
		if ev.Status != schemas.EvaluationDone {
			r.Caveats = append(r.Caveats, "The evaluation ended "+ev.Status+": the numbers cover the runs that finished.")
		}
		return r
	}
	r.Action = ActionStillMoving
	r.Strength = StrengthWeak
	r.Summary = "The evaluation is still running; this is a partial reading. " + r.Summary
	return r
}

func comparisonOf(rep *Report, label string) (Comparison, bool) {
	i := slices.IndexFunc(rep.Comparisons, func(c Comparison) bool { return c.Label == label })
	if i < 0 {
		return Comparison{}, false
	}
	return rep.Comparisons[i], true
}

func qualityLine(m Model) string {
	q, ok := m.Quality.Float()
	if !ok {
		return fmt.Sprintf("%s has no graded run", display(m))
	}
	s := fmt.Sprintf("%s scored %.0f%%", display(m), q*100)
	if b := m.Quality.Bounds; b != nil {
		s += fmt.Sprintf(" (range %.0f%% to %.0f%%)", b[0]*100, b[1]*100)
	}
	return s + fmt.Sprintf(" on %d task(s), %d run(s)", m.Tasks, m.Runs)
}

func costLine(a, b Model) string {
	ca, okA := a.CostPerSuccess.Float()
	cb, okB := b.CostPerSuccess.Float()
	switch {
	case !okA && !okB:
		return fmt.Sprintf("Cost per verified success is unknown for both %s and %s: %s", display(a), display(b), a.CostPerSuccess.Reason)
	case !okA:
		return fmt.Sprintf("Cost per verified success of %s is unknown (%s); %s costs %s.", display(a), a.CostPerSuccess.Reason, display(b), usd(cb))
	case !okB:
		return fmt.Sprintf("%s costs %s per verified success; the cost of %s is unknown (%s).", display(a), usd(ca), display(b), b.CostPerSuccess.Reason)
	}
	s := fmt.Sprintf("%s costs %s per verified success against %s for %s", display(a), usd(ca), usd(cb), display(b))
	switch {
	case ca > 0 && cb > ca:
		s += fmt.Sprintf(", %.1f times less", cb/ca)
	case cb > 0 && ca > cb:
		s += fmt.Sprintf(", %.1f times more", ca/cb)
	}
	return s + "."
}

func latencyLine(a, b Model) string {
	la, okA := a.LatencyP50MS.Float()
	lb, okB := b.LatencyP50MS.Float()
	if !okA || !okB {
		return ""
	}
	return fmt.Sprintf("Median model call latency: %.0f ms for %s against %.0f ms for %s.", la, display(a), lb, display(b))
}

func usd(x float64) string {
	switch {
	case x >= 1:
		return fmt.Sprintf("$%.2f", x)
	case x >= 0.01:
		return fmt.Sprintf("$%.3f", x)
	}
	return fmt.Sprintf("$%.4f", x)
}

func joinWords(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	out := ""
	for i, x := range xs {
		switch {
		case i == 0:
			out = x
		case i == len(xs)-1:
			out += " and " + x
		default:
			out += ", " + x
		}
	}
	return out
}
