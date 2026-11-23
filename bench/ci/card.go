package ci

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// First workflow: the verdict card. `heimdall compare` prints it and the
// workflow `heimdall ci install github` writes posts it as one pull-request
// comment, updated in place (CardMarker finds the comment again).

// CardMarker is the hidden first line of every card; the PR comment step
// edits the comment that holds it instead of adding a new one.
const CardMarker = "<!-- heimdall-verdict -->"

// CardInput is what the card shows beside the analysis.
type CardInput struct {
	ArmLabels  map[string]string // arm id -> label
	Unreviewed int               // tasks whose instruction a tool drafted and nobody reviewed
	Tasks      int               // tasks in the experiment
	UILink     string            // verdict page; "" leaves the line out
	Evidence   string            // where the sealed runs are (a path or an artifact name)
	Subject    string            // what was compared, e.g. "main..HEAD"; "" leaves it out
}

// VerdictCard renders the analysis as a short markdown card: the decision
// and action, a data-strength banner (synthetic or recorded), the unreviewed
// instruction count, success and cost per verified success for both arms,
// the effect with its interval, the policy, integrity, invalid reasons, the
// three tasks whose outcome differs most, and the links. It is pure.
func VerdictCard(an *schemas.Analysis, in CardInput) string {
	var b strings.Builder
	v := an.Verdict
	b.WriteString(CardMarker + "\n")
	fmt.Fprintf(&b, "### Heimdall verdict: %s (%s)\n\n", strings.ToUpper(string(v.Decision)), actionText(v.Action))
	if in.Subject != "" {
		fmt.Fprintf(&b, "Compared: %s\n\n", in.Subject)
	}
	switch an.Mode {
	case schemas.ModeSynthetic:
		b.WriteString("> **Synthetic data**: scripted agents or a scripted model. This shows the pipeline works; it is not a finding about any agent.\n")
	case schemas.ModeRecorded:
		b.WriteString("> **Recorded data**: re-analysis of sealed runs; no agent ran for this card.\n")
	}
	if in.Tasks > 0 {
		fmt.Fprintf(&b, "> %d of %d tasks have unreviewed instructions", in.Unreviewed, in.Tasks)
		if in.Unreviewed > 0 {
			b.WriteString(" (drafted by `tasks mine`; review them with `heimdall task review`)")
		}
		b.WriteString(".\n")
	}
	b.WriteString("\n")

	arms := armIDs(an)
	label := func(id string) string {
		if l := in.ArmLabels[id]; l != "" {
			return id + ": " + mdEscape(l)
		}
		return id
	}
	b.WriteString("| |")
	for _, id := range arms {
		fmt.Fprintf(&b, " %s |", label(id))
	}
	b.WriteString("\n|---|")
	for range arms {
		b.WriteString("---|")
	}
	cond := v.Primary.Condition
	if cond == "" {
		cond = schemas.ConditionClean
	}
	fmt.Fprintf(&b, "\n| verified success (%s) |", cond)
	row := primaryRow(an, cond)
	for _, id := range arms {
		fmt.Fprintf(&b, " %s |", ratioText(row[id]))
	}
	b.WriteString("\n| cost per verified success |")
	cost := meter(an, "cost_per_verified_success")
	for _, id := range arms {
		fmt.Fprintf(&b, " %s |", moneyValue(cost[id]))
	}
	b.WriteString("\n\n")

	if sv := v.Setups; sv != nil { // fix wave 3, N244: three or more setups have no B-against-A story
		b.WriteString(setupsText(v.Decision, sv) + "\n\n")
	} else if e := v.Effect; e != nil {
		fmt.Fprintf(&b, "**Effect** of B against A on %s (%s): %s", e.Metric, e.Condition, effectText(e))
		b.WriteString("\n\n")
	} else {
		b.WriteString("**Effect**: none computed.\n\n")
	}
	if v.Decision == schemas.DecisionInconclusive && v.TasksNeeded > 0 {
		fmt.Fprintf(&b, "Inconclusive: about %d tasks would be needed to detect a difference this size.\n\n", v.TasksNeeded)
	}
	if len(v.InvalidReasons) > 0 {
		b.WriteString("**Invalid because**:\n")
		for i, r := range v.InvalidReasons {
			if i == 5 {
				fmt.Fprintf(&b, "- and %d more\n", len(v.InvalidReasons)-5)
				break
			}
			fmt.Fprintf(&b, "- %s\n", mdEscape(r))
		}
		b.WriteString("\n")
	}
	if p := an.Policy; p != nil {
		failing := 0
		for _, c := range p.Checks {
			if c.Status != schemas.PolicyPass {
				failing++
			}
		}
		fmt.Fprintf(&b, "**Policy** %s: %s", mdEscape(p.RuleSetName()), p.Decision) // fix wave 3, N61: the rule set by its file name
		if failing > 0 {
			fmt.Fprintf(&b, " (%d of %d checks not passing)", failing, len(p.Checks))
		}
		b.WriteString("  \n")
	}
	fmt.Fprintf(&b, "**Integrity**: %s\n\n", v.Integrity)

	if top := topTasks(v.Effect, 3); len(top) > 0 {
		b.WriteString("**Tasks that differ most**\n\n| task | A | B | B - A |\n|---|---|---|---|\n")
		for _, d := range top {
			fmt.Fprintf(&b, "| `%s` | %.2f | %.2f | %+.2f |\n", d.Task, d.MeanA, d.MeanB, d.D)
		}
		b.WriteString("\n")
	}
	var links []string
	if in.UILink != "" {
		links = append(links, fmt.Sprintf("[verdict page](%s)", in.UILink))
	}
	if in.Evidence != "" {
		links = append(links, "evidence: "+in.Evidence)
	}
	fmt.Fprintf(&b, "Experiment `%s`, analysis `%s`", an.ExpID, an.AnalysisID)
	if len(links) > 0 {
		b.WriteString("; " + strings.Join(links, "; "))
	}
	b.WriteString("\n")
	return b.String()
}

// setupsText is the result line of a comparison of three or more setups:
// the leader, or the setups that cannot be told apart yet, with the tasks
// where a setup broke a safety rule.
func setupsText(d schemas.Decision, sv *schemas.SetupsVerdict) string {
	var s string
	switch {
	case d == schemas.DecisionLeader:
		s = "**Setups**: " + mdEscape(sv.Leader) + " leads every other setup"
		if len(sv.Behind) > 0 {
			s += " (" + mdEscape(strings.Join(sv.Behind, ", ")) + " behind)"
		}
	case len(sv.TopGroup) > 1:
		s = "**Setups**: cannot tell " + mdEscape(strings.Join(sv.TopGroup, ", ")) + " apart yet"
	default:
		s = "**Setups**: no leader yet"
	}
	s += "."
	if sv.Note != "" {
		s += " " + mdEscape(sv.Note)
	}
	for _, arm := range slices.Sorted(maps.Keys(sv.Safety)) {
		s += fmt.Sprintf(" %s broke a safety rule on %s.", mdEscape(arm), mdEscape(strings.Join(sv.Safety[arm], ", ")))
	}
	return s
}

func actionText(a schemas.Action) string {
	switch a {
	case schemas.ActionShip:
		return "ship"
	case schemas.ActionHold:
		return "hold"
	case schemas.ActionFixHarness:
		return "fix the harness"
	case schemas.ActionNoDecision:
		return "no decision"
	}
	if a == "" {
		return "no action"
	}
	return string(a)
}

func armIDs(an *schemas.Analysis) []string {
	seen := map[string]bool{"A": true, "B": true}
	for id := range an.Naive {
		seen[id] = true
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// primaryRow is the per-arm success ratio on the primary condition, from the
// diagnostics, else the naive scoreboard.
func primaryRow(an *schemas.Analysis, cond string) map[string]schemas.Ratio {
	for _, d := range an.Diagnostics {
		if d.Condition == cond && len(d.PerArm) > 0 {
			return d.PerArm
		}
	}
	return an.Naive
}

func meter(an *schemas.Analysis, name string) map[string]schemas.Value {
	for _, m := range an.Meters {
		if m.Name == name {
			return m.PerArm
		}
	}
	return nil
}

func ratioText(r schemas.Ratio) string {
	if r.Den == 0 && r.Unknown == 0 {
		return "no runs"
	}
	s := fmt.Sprintf("%d/%d", r.Num, r.Den)
	if r.Den > 0 {
		s += fmt.Sprintf(" (%.0f%%)", 100*float64(r.Num)/float64(r.Den))
	}
	if r.Unknown > 0 {
		s += fmt.Sprintf(", %d unknown", r.Unknown)
	}
	return s
}

func moneyValue(v schemas.Value) string {
	if v.V == nil {
		if v.Reason != "" {
			return "unknown (" + mdEscape(v.Reason) + ")"
		}
		return "unknown"
	}
	return fmt.Sprintf("$%.4f", *v.V)
}

// effectText is the mean paired difference with its interval and p-value,
// in percentage points for a 0/1 success metric.
func effectText(e *schemas.Effect) string {
	pp := e.Metric == "verified_success" || strings.HasSuffix(e.Metric, "_rate")
	num := func(x float64) string {
		if pp {
			return fmt.Sprintf("%+.1f pp", 100*x)
		}
		return fmt.Sprintf("%+.3g", x)
	}
	s := num(e.DBar)
	if e.Interval != nil && !math.IsNaN(e.Interval[0]) && !math.IsNaN(e.Interval[1]) {
		s += fmt.Sprintf(", interval [%s, %s]", num(e.Interval[0]), num(e.Interval[1]))
	}
	if e.PValue != nil {
		s += fmt.Sprintf(", sign-flip p = %.3g", *e.PValue)
	}
	s += fmt.Sprintf(" over %d tasks (%d better, %d worse, %d tied)", e.NTasks, e.NPositive, e.NNegative, e.NTasks-e.NNonzero)
	if e.Descriptive {
		s += "; descriptive only"
	}
	return s
}

// topTasks returns the n tasks with the largest absolute paired difference,
// ties broken by task id; tasks with no difference are left out.
func topTasks(e *schemas.Effect, n int) []schemas.TaskDiff {
	if e == nil {
		return nil
	}
	var out []schemas.TaskDiff
	for _, d := range e.PerTask {
		if d.D != 0 {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if math.Abs(out[i].D) != math.Abs(out[j].D) {
			return math.Abs(out[i].D) > math.Abs(out[j].D)
		}
		return out[i].Task < out[j].Task
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// mdEscape keeps a free-text value from breaking the table or the comment.
func mdEscape(s string) string {
	r := strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "<!--", "&lt;!--")
	return r.Replace(s)
}
