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

func judge(rep *reportcard.Report, o GateOptions) (string, []string) {
	var regress []string
	if ov := rep.Overall; ov != nil && ov.Significant && ov.Winner == ArmLive {
		regress = append(regress, fmt.Sprintf("worse overall by %s per task", pts(ov.Delta)))
	}
	for _, b := range rep.Benchmarks {
		live, pr := b.Arms[ArmLive], b.Arms[ArmPR]
		if v := b.Verdict; v != nil && v.Significant && v.Winner == ArmLive {
			regress = append(regress, fmt.Sprintf("%s: worse by %s, and the gap is real", b.Benchmark, pts(v.Delta)))
			continue
		}
		if o.MaxScoreDrop > 0 && live.Runs > 0 && pr.Runs > 0 && live.Score-pr.Score > o.MaxScoreDrop+1e-9 {
			regress = append(regress, fmt.Sprintf("%s: score fell by %s, over the %s limit", b.Benchmark, pts(live.Score-pr.Score), pts(o.MaxScoreDrop)))
		}
	}
	if o.MaxCostIncrease > 0 {
		liveCost, prCost := totalCost(rep, ArmLive), totalCost(rep, ArmPR)
		if liveCost > 0 && prCost > liveCost*(1+o.MaxCostIncrease)+1e-9 {
			regress = append(regress, fmt.Sprintf("cost rose %.0f%% ($%.2f to $%.2f), over the %.0f%% limit",
				(prCost/liveCost-1)*100, liveCost, prCost, o.MaxCostIncrease*100))
		}
	}
	if len(regress) > 0 {
		return OutcomeRegressed, regress
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

func totalCost(rep *reportcard.Report, arm string) float64 {
	s := 0.0
	for _, b := range rep.Benchmarks {
		s += b.Arms[arm].CostUSD
	}
	return s
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
		fmt.Fprintf(&b, "| Benchmark | Live v%d | This PR v%d | Change | Verdict |\n", in.LiveVersion, in.PRVersion)
		b.WriteString("|---|---|---|---|---|\n")
		bs := append([]reportcard.Benchmark(nil), rep.Benchmarks...)
		sort.SliceStable(bs, func(i, j int) bool { return bs[i].Benchmark < bs[j].Benchmark })
		for _, x := range bs {
			live, pr := x.Arms[ArmLive], x.Arms[ArmPR]
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", mdCell(x.Benchmark), scoreCell(live), scoreCell(pr), changeCell(live, pr), verdictCell(x.Verdict))
		}
		liveCost, prCost := totalCost(rep, ArmLive), totalCost(rep, ArmPR)
		if liveCost > 0 || prCost > 0 {
			fmt.Fprintf(&b, "| Cost | $%.2f | $%.2f | %s | |\n", liveCost, prCost, costChange(liveCost, prCost))
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

func verdictCell(v *verdictab.BenchmarkVerdict) string {
	if v == nil {
		return ""
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
