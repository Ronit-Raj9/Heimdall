package ci

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/verdictab"
)

// Hosted CI (`bifrost-bench ci evaluate`): a pull request's agent config is
// registered as a new version on a hosted Heimdall, evaluated against the
// live version, and the evaluation gate below turns the report card into a
// pass or a fail. The gate is pure; the command does the HTTP.

// Gate outcomes.
const (
	OutcomeImproved     = "improved"     // the pull request is better overall, and the gap is real
	OutcomeSame         = "same"         // no real gap either way
	OutcomeInconclusive = "inconclusive" // too few runs to tell
	OutcomeRegressed    = "regressed"    // worse overall or on a benchmark, or over a limit
	OutcomeFailed       = "failed"       // the evaluation failed, was cancelled or ran nothing
	OutcomeNoChange     = "no-change"    // the pull request's config is the live version
)

// Gate exit codes, the same numbers `check` uses.
const (
	GateExitPass         = 0
	GateExitRegressed    = 1
	GateExitInconclusive = 2
	GateExitFailed       = 3
)

// Evaluation arm labels: the live version is A, the pull request B.
const (
	ArmLive = verdictab.WinnerA
	ArmPR   = verdictab.WinnerB
)

// DefaultFailOn is what fails the gate unless told otherwise.
var DefaultFailOn = []string{OutcomeRegressed, OutcomeFailed}

// GateOptions are the evaluation gate's rules.
type GateOptions struct {
	FailOn []string // outcomes that fail: regressed, inconclusive, failed
	// MaxScoreDrop fails a benchmark whose score falls by more than this many
	// points (0.05 = 5 points) even when the gap could be chance; 0 is off.
	MaxScoreDrop float64
	// MaxCostIncrease fails when the pull request's total cost grows by more
	// than this fraction of the live version's (0.25 = 25%); 0 is off.
	MaxCostIncrease float64
}

// GateResult is the gate's decision.
type GateResult struct {
	Outcome  string   `json:"outcome"`
	Pass     bool     `json:"pass"`
	ExitCode int      `json:"exit_code"`
	Reasons  []string `json:"reasons"`
}

// ParseFailOn reads a comma-separated fail-on list and refuses an unknown
// outcome.
func ParseFailOn(s string) ([]string, error) {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		switch f {
		case "":
			continue
		case OutcomeRegressed, OutcomeInconclusive, OutcomeFailed:
			out = append(out, f)
		default:
			return nil, fmt.Errorf("fail-on %q: want regressed, inconclusive or failed", f)
		}
	}
	return out, nil
}

// EvaluateGate decides the gate from the evaluation's final status and its
// report card (arm A the live version, arm B the pull request).
func EvaluateGate(status string, rep *reportcard.Report, o GateOptions) GateResult {
	res := GateResult{Outcome: OutcomeInconclusive}
	ran := 0
	if rep != nil {
		for _, b := range rep.Benchmarks {
			if b.Arms[ArmLive].Runs > 0 || b.Arms[ArmPR].Runs > 0 {
				ran++
			}
		}
	}
	switch {
	case status == "failed" || status == "cancelled":
		res.Outcome = OutcomeFailed
		res.Reasons = append(res.Reasons, "the evaluation "+status)
	case rep == nil || ran == 0:
		res.Outcome = OutcomeFailed
		res.Reasons = append(res.Reasons, "no benchmark produced runs")
	default:
		res.Outcome, res.Reasons = judge(rep, o)
	}
	res.Pass = !contains(o.FailOn, res.Outcome)
	if !res.Pass {
		switch res.Outcome {
		case OutcomeRegressed:
			res.ExitCode = GateExitRegressed
		case OutcomeInconclusive:
			res.ExitCode = GateExitInconclusive
		default:
			res.ExitCode = GateExitFailed
		}
	}
	return res
}

// judge reads the report card (G4-07). A benchmark regresses when its
// paired drop is real: the A/B verdict is significant, or the 95% paired
// bootstrap interval of the change lies below zero. With max-score-drop set
// the drop must also be larger than it; a drop over the limit the interval
// cannot tell from noise makes the outcome inconclusive, never regressed.
// A benchmark whose judge is not calibrated has its verdict capped on the
// report card and is never a regression here.
func judge(rep *reportcard.Report, o GateOptions) (string, []string) {
	var regress, unsure []string
	if ov := rep.Overall; ov != nil && ov.Significant && ov.Winner == ArmLive && (o.MaxScoreDrop <= 0 || -ov.Delta > o.MaxScoreDrop+1e-9) {
		regress = append(regress, fmt.Sprintf("worse overall by %s per task", pts(ov.Delta)))
	}
	for _, b := range rep.Benchmarks {
		v := b.Verdict
		if v == nil || v.JudgeCapped || v.Tasks == 0 {
			continue
		}
		pc := verdictab.Paired(v.TaskDiffs)
		drop := -v.Delta
		real := (v.Significant && v.Winner == ArmLive) || (pc.OK && pc.Hi < 0)
		over := o.MaxScoreDrop <= 0 || drop > o.MaxScoreDrop+1e-9
		switch {
		case real && over && o.MaxScoreDrop > 0:
			regress = append(regress, fmt.Sprintf("%s: score fell by %s%s, a real drop over the %s limit", b.Benchmark, pts(drop), intervalText(pc), pts(o.MaxScoreDrop)))
		case real && over && v.Significant && v.Winner == ArmLive:
			regress = append(regress, fmt.Sprintf("%s: worse by %s, and the gap is real", b.Benchmark, pts(v.Delta)))
		case o.MaxScoreDrop > 0 && over && drop > 0:
			unsure = append(unsure, fmt.Sprintf("%s: score fell by %s%s, over the %s limit, but that could be noise; %s", b.Benchmark, pts(drop), intervalText(pc), pts(o.MaxScoreDrop), mdeText(v.Tasks)))
		}
	}
	if o.MaxCostIncrease > 0 {
		liveCost, liveKnown := totalCost(rep, ArmLive)
		prCost, prKnown := totalCost(rep, ArmPR)
		if liveKnown && prKnown && liveCost > 0 && prCost > liveCost*(1+o.MaxCostIncrease)+1e-9 {
			regress = append(regress, fmt.Sprintf("cost rose %.0f%% ($%.2f to $%.2f), over the %.0f%% limit",
				(prCost/liveCost-1)*100, liveCost, prCost, o.MaxCostIncrease*100))
		}
	}
	if len(regress) > 0 {
		return OutcomeRegressed, regress
	}
	if len(unsure) > 0 {
		return OutcomeInconclusive, unsure
	}
	ov := rep.Overall
	switch {
	case ov != nil && ov.Significant && ov.Winner == ArmPR:
		return OutcomeImproved, []string{fmt.Sprintf("better overall by %s per task, and the gap is real", pts(ov.Delta))}
	case ov != nil && ov.Winner == verdictab.WinnerTie:
		return OutcomeSame, []string{"about the same as the live version on every benchmark"}
	}
	return OutcomeInconclusive, []string{"no real gap yet: more tasks or trials would settle it"}
}

// intervalText is " (95% interval -20 pts to -2 pts)", "" without one.
func intervalText(pc verdictab.PairedChange) string {
	if !pc.OK {
		return ""
	}
	return fmt.Sprintf(" (95%% interval %s to %s)", signedPts(pc.Lo), signedPts(pc.Hi))
}

// mdeText is the smallest change n paired tasks can detect, in words.
func mdeText(n int) string {
	m := verdictab.MDE(n)
	if math.IsInf(m, 0) || math.IsNaN(m) {
		return fmt.Sprintf("%d paired task is too few to detect any change", n)
	}
	return fmt.Sprintf("%d paired tasks can detect a change of about %s", n, pts(m))
}

// totalCost sums arm's cost over every benchmark; known is false when any
// benchmark's cost is not known (an unpriced model), and the sum is then
// never used: a partial sum would read as the full cost.
func totalCost(rep *reportcard.Report, arm string) (total float64, known bool) {
	known = true
	for _, b := range rep.Benchmarks {
		m := b.Arms[arm]
		if m.CostUSD == nil {
			if m.Runs > 0 {
				known = false
			}
			continue
		}
		total += *m.CostUSD
	}
	return total, known
}

// costCell is a cost for the comment table, "not known" when it is not.
func costCell(c float64, known bool) string {
	if !known {
		return "not known"
	}
	return fmt.Sprintf("$%.2f", c)
}

func pts(d float64) string {
	p := math.Round(math.Abs(d) * 100)
	if p == 1 {
		return "1 point"
	}
	return fmt.Sprintf("%.0f points", p)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// EvaluationMarker is the hidden first line of the pull-request comment for
// one agent; later runs edit that comment instead of adding one.
func EvaluationMarker(agent string) string {
	return "<!-- heimdall-evaluation agent=" + agent + " -->"
}

// CommentInput is what the pull-request comment shows.
type CommentInput struct {
	Agent       string
	LiveVersion int
	PRVersion   int
	Status      string // evaluation status
	Report      *reportcard.Report
	Gate        GateResult
	ReportURL   string // the report card on the hosted Heimdall; "" leaves it out
	Commit      string // the pull request's head commit
	Note        string // a line under the heading, e.g. the timeout
}

// EvaluationComment renders the report card summary as markdown for a pull
// request comment: the gate, a score table per benchmark (live version vs
// the pull request), how each holds up under faults, cost, and the link. It
// is pure.
func EvaluationComment(in CommentInput) string {
	var b strings.Builder
	b.WriteString(EvaluationMarker(in.Agent) + "\n")
	verdict := "passed"
	if !in.Gate.Pass {
		verdict = "failed"
	}
	fmt.Fprintf(&b, "### Heimdall: %s v%d (this pull request) vs v%d (live)\n\n", in.Agent, in.PRVersion, in.LiveVersion)
	fmt.Fprintf(&b, "**Gate %s: %s.**", verdict, outcomeText(in.Gate.Outcome))
	if len(in.Gate.Reasons) > 0 {
		b.WriteString(" Why: " + strings.Join(in.Gate.Reasons, "; ") + ".")
	}
	b.WriteString("\n\n")
	if in.Note != "" {
		b.WriteString("> " + in.Note + "\n\n")
	}
	rep := in.Report
	if rep != nil && len(rep.Benchmarks) > 0 {
		fmt.Fprintf(&b, "| Benchmark | Live v%d | This PR v%d | Change | Verdict | Graded by |\n", in.LiveVersion, in.PRVersion)
		b.WriteString("|---|---|---|---|---|---|\n")
		bs := append([]reportcard.Benchmark(nil), rep.Benchmarks...)
		sort.SliceStable(bs, func(i, j int) bool { return bs[i].Benchmark < bs[j].Benchmark })
		for _, x := range bs {
			live, pr := x.Arms[ArmLive], x.Arms[ArmPR]
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", mdCell(x.Benchmark), scoreCell(live), scoreCell(pr), changeCell(live, pr), verdictCell(x.Verdict), gradedByCell(x.Judge))
		}
		liveCost, liveKnown := totalCost(rep, ArmLive)
		prCost, prKnown := totalCost(rep, ArmPR)
		switch {
		case liveKnown && prKnown && (liveCost > 0 || prCost > 0):
			fmt.Fprintf(&b, "| Cost | $%.2f | $%.2f | %s | |\n", liveCost, prCost, costChange(liveCost, prCost))
		case !liveKnown || !prKnown:
			fmt.Fprintf(&b, "| Cost | %s | %s | | |\n", costCell(liveCost, liveKnown), costCell(prCost, prKnown))
		}
		b.WriteString("\n")
	}
	if rep != nil && len(rep.Reliability) > 0 {
		b.WriteString("Under faults (score, change from clean):\n\n")
		for _, r := range rep.Reliability {
			live, pr := r.Arms[ArmLive], r.Arms[ArmPR]
			fmt.Fprintf(&b, "- %s: live %s (%s), this PR %s (%s)\n", mdCell(r.Fault), pct(live.Score), signedPts(live.Drop), pct(pr.Score), signedPts(pr.Drop))
		}
		b.WriteString("\n")
	}
	if rep != nil && rep.Overall != nil && rep.Overall.Benchmarks > 0 {
		w := rep.Overall.Wins
		fmt.Fprintf(&b, "Benchmarks won: this PR %d, live %d, tied %d, unclear %d.\n\n", w[ArmPR], w[ArmLive], w[verdictab.WinnerTie], w[verdictab.WinnerUnclear])
	}
	if line := sizeLine(rep); line != "" {
		b.WriteString(line + "\n\n")
	}
	var foot []string
	if in.ReportURL != "" {
		foot = append(foot, fmt.Sprintf("[Open the full report card](%s)", in.ReportURL))
	}
	if rep != nil && rep.Evaluation != "" {
		foot = append(foot, "evaluation `"+rep.Evaluation+"`")
	}
	if in.Commit != "" {
		c := in.Commit
		if len(c) > 12 {
			c = c[:12]
		}
		foot = append(foot, "commit `"+c+"`")
	}
	if len(foot) > 0 {
		b.WriteString(strings.Join(foot, " · ") + "\n")
	}
	return b.String()
}

// NoChangeComment is the comment when the pull request leaves the agent's
// config as the live version: there is nothing to evaluate.
func NoChangeComment(agent string, live int, commit string) string {
	var b strings.Builder
	b.WriteString(EvaluationMarker(agent) + "\n")
	fmt.Fprintf(&b, "### Heimdall: %s\n\n", agent)
	fmt.Fprintf(&b, "**Gate passed: no agent change.** This pull request's config is the live version v%d, so there is nothing to evaluate.\n", live)
	if commit != "" {
		if len(commit) > 12 {
			commit = commit[:12]
		}
		fmt.Fprintf(&b, "\ncommit `%s`\n", commit)
	}
	return b.String()
}

// StatusDescription is the one-line commit status text (at most 140
// characters, GitHub's limit).
func StatusDescription(g GateResult) string {
	s := outcomeText(g.Outcome)
	if len(g.Reasons) > 0 {
		s += ": " + g.Reasons[0]
	}
	if len(s) > 140 {
		s = s[:137] + "..."
	}
	return capitalize(s)
}

func outcomeText(o string) string {
	switch o {
	case OutcomeImproved:
		return "better than the live version"
	case OutcomeSame:
		return "same as the live version"
	case OutcomeRegressed:
		return "worse than the live version"
	case OutcomeFailed:
		return "the evaluation did not finish"
	case OutcomeNoChange:
		return "no agent change"
	}
	return "no clear difference"
}

func scoreCell(m reportcard.Metrics) string {
	if m.Runs == 0 {
		return "no runs"
	}
	return fmt.Sprintf("%s (%s to %s, %d runs)", pct(m.Score), pct(m.CILow), pct(m.CIHigh), m.Runs)
}

func changeCell(live, pr reportcard.Metrics) string {
	if live.Runs == 0 || pr.Runs == 0 {
		return ""
	}
	return signedPts(pr.Score - live.Score)
}

// gradedByCell is how a benchmark is graded: a check, or a model judge
// with its calibration state.
func gradedByCell(j *reportcard.JudgeState) string {
	if j == nil {
		return "check"
	}
	return "judge, " + mdCell(j.Label)
}

// sizeLine says the smallest change the evaluation can detect per
// benchmark, from the smallest paired task count.
func sizeLine(rep *reportcard.Report) string {
	if rep == nil {
		return ""
	}
	n := 0
	for _, b := range rep.Benchmarks {
		if v := b.Verdict; v != nil && v.Tasks > 0 && (n == 0 || v.Tasks < n) {
			n = v.Tasks
		}
	}
	if n == 0 {
		return ""
	}
	return "Size: " + mdeText(n) + " on a benchmark (smaller gaps read as unclear)."
}

func verdictCell(v *verdictab.BenchmarkVerdict) string {
	if v == nil {
		return ""
	}
	if v.JudgeCapped {
		return "unclear (judge not calibrated)"
	}
	switch v.Winner {
	case ArmPR:
		if v.Significant {
			return "better"
		}
	case ArmLive:
		if v.Significant {
			return "**worse**"
		}
	case verdictab.WinnerTie:
		return "same"
	}
	return "unclear"
}

func costChange(live, pr float64) string {
	if live <= 0 {
		return ""
	}
	return fmt.Sprintf("%+.0f%%", (pr/live-1)*100)
}

func pct(x float64) string { return fmt.Sprintf("%.0f%%", x*100) }

func signedPts(d float64) string {
	p := math.Round(d * 100)
	if p == 0 {
		return "0 pts"
	}
	return fmt.Sprintf("%+.0f pts", p)
}

func mdCell(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s) }

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
