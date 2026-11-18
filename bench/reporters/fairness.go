package reporters

import (
	"bufio"
	"fmt"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// claimSentence is the rule every fairness section ends with: a comparison
// of topologies says nothing about multi-agent systems unless the two cheap
// single-agent baselines ran at the same spend (docs/multi-agent.md).
const claimSentence = "A claim that multi-agent is better needs a single-agent baseline and a best-of-N single-agent baseline at the same spend"

// writeFairness writes the Fairness section of an experiment that declared a
// fairness rule (verdict.Fairness): the rule, each arm's cap and topology,
// the runs that went over their cap, whether each baseline was present and
// whether a multi-agent claim is allowed. An experiment without a rule
// writes nothing, so its report is the same as before fairness existed.
func writeFairness(w *bufio.Writer, f *schemas.FairnessReport) {
	if f == nil {
		return
	}
	w.WriteString("## Fairness\n\n| Field | Value |\n|---|---|\n")
	row(w, "Rule", orUnknown(f.Rule))
	row(w, "Caps (USD)", armValues(f.CapsUSD, func(v float64) string { return fixed(v, 2) }))
	if len(f.Topologies) > 0 {
		row(w, "Topologies", armValues(f.Topologies, func(v string) string { return v }))
	}
	exceeded := "none"
	if len(f.Exceeded) > 0 {
		exceeded = strings.Join(f.Exceeded, ", ")
	}
	row(w, "Runs over their arm's cap", exceeded)
	row(w, "Single-agent baseline", yesNo(f.SingleAgentBaseline))
	row(w, "Best-of-N baseline", yesNo(f.BestOfNBaseline))
	row(w, "Claim allowed", yesNo(f.ClaimAllowed))
	w.WriteString("\n")
	for _, n := range f.Notes {
		fmt.Fprintf(w, "- %s\n", cell(n))
	}
	if len(f.Notes) > 0 {
		w.WriteString("\n")
	}
	present := "missing"
	if f.SingleAgentBaseline && f.BestOfNBaseline {
		present = "present"
	}
	fmt.Fprintf(w, "%s: %s.\n\n", claimSentence, present)
}

// armValues writes "arm value" pairs in arm order, comma separated.
func armValues[V any](m map[string]V, text func(V) string) string {
	if len(m) == 0 {
		return unknownText
	}
	arms := make([]string, 0, len(m))
	for arm := range m {
		arms = append(arms, arm)
	}
	slices.Sort(arms)
	parts := make([]string, 0, len(arms))
	for _, arm := range arms {
		parts = append(parts, arm+" "+text(m[arm]))
	}
	return strings.Join(parts, ", ")
}

// yesNo is "yes" or "no".
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
