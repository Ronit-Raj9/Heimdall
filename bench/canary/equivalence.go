package canary

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/stats"
)

// Equivalence verdicts.
const (
	EquivDiffers      = "differs"                // some metric differs at the Holm level
	EquivNoDifference = "no_difference_detected" // enough changed tasks to test; nothing detected (not proof of sameness)
	EquivCannotTell   = "cannot_tell"            // too few tasks differ for any test to reach the level
)

// EquivalenceMetrics are compared between two providers of one model.
var EquivalenceMetrics = []string{MetricSuccess, MetricCost, MetricLatency, MetricFormatErrors}

// Equivalence is the paired comparison of the same model served through two
// providers in one experiment: per task, provider B minus provider A, on
// success, cost, latency and tool-call format errors, Holm over the four.
type Equivalence struct {
	Model   string       `json:"model"`
	ArmA    string       `json:"arm_a"`
	ArmB    string       `json:"arm_b"`
	LabelA  string       `json:"label_a"`
	LabelB  string       `json:"label_b"`
	Tests   []MetricTest `json:"tests"`
	Verdict string       `json:"verdict"`
	Reason  string       `json:"reason"`
}

// bareModel drops one leading provider segment ("anthropic/claude-x").
func bareModel(m string) string {
	if _, rest, ok := strings.Cut(m, "/"); ok {
		return rest
	}
	return m
}

// EquivalencePairs returns the arm pairs of a definition that watch the
// same model through different providers, in target order.
func EquivalencePairs(d *Definition) [][2]int {
	var out [][2]int
	for i := range d.Targets {
		for j := i + 1; j < len(d.Targets); j++ {
			a, b := d.Targets[i], d.Targets[j]
			if bareModel(a.Model) == bareModel(b.Model) && a.Provider != b.Provider {
				out = append(out, [2]int{i, j})
			}
		}
	}
	return out
}

// CompareProviders builds the equivalence report of every pair in one
// execution. alpha is the family level; no noise floor applies (any
// detected difference between providers is reported).
func CompareProviders(d *Definition, x *Execution, alpha float64) []Equivalence {
	var out []Equivalence
	for _, p := range EquivalencePairs(d) {
		a, b := ArmOf(p[0]), ArmOf(p[1])
		// Treat arm A's runs as the "baseline" side and B's as "current",
		// relabelled to one arm so taskMeans pairs them by task.
		var ra, rb []RunObs
		for _, r := range x.Runs {
			switch r.Arm {
			case a:
				r.Arm = "pair"
				ra = append(ra, r)
			case b:
				r.Arm = "pair"
				rb = append(rb, r)
			}
		}
		labels := map[string]string{"pair": d.Targets[p[1]].Label() + " vs " + d.Targets[p[0]].Label()}
		c := Compare(ra, rb, labels, EquivalenceMetrics, alpha, func(string) float64 { return 0 })
		e := Equivalence{Model: bareModel(d.Targets[p[0]].Model), ArmA: a, ArmB: b, LabelA: d.Targets[p[0]].Label(), LabelB: d.Targets[p[1]].Label(), Tests: c.Tests}
		var differs, tested []string
		for _, t := range c.Tests {
			switch t.Status {
			case TestWorse, TestBetter, TestBelowFloor:
				differs = append(differs, fmt.Sprintf("%s on %s: %s", t.Metric, t.Condition, changeText(t)))
			case TestNoChange:
				// Unchanged counts as evidence only when enough tasks were
				// compared that a one-way change could have reached the level.
				if stats.MinPossibleP(t.Tasks) <= alpha {
					tested = append(tested, t.Metric)
				}
			}
		}
		switch {
		case len(differs) > 0:
			e.Verdict, e.Reason = EquivDiffers, fmt.Sprintf("%s differs from %s: %s", e.LabelB, e.LabelA, strings.Join(differs, "; "))
		case len(tested) > 0:
			e.Verdict, e.Reason = EquivNoDifference, fmt.Sprintf("no difference detected on %s at family level %.2f; with this many tasks only large differences are detectable, so this is not proof the providers are the same", strings.Join(tested, ", "), alpha)
		default:
			e.Verdict, e.Reason = EquivCannotTell, "too few tasks differ between the providers for the sign-flip test to reach the level: cannot tell"
		}
		out = append(out, e)
	}
	return out
}
