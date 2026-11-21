package monitorcert

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RenderMarkdown renders a certificate as Markdown: the monitor, the
// labelled sets, every operating point and family, warnings, the integrity
// check and the replayed runs. It is a pure function of the certificate.
func RenderMarkdown(c *schemas.MonitorCertificate) []byte {
	var w bytes.Buffer
	fmt.Fprintf(&w, "# Monitor certificate %s\n\n", c.CertID)
	fmt.Fprintf(&w, "Monitor `%s` (kind %s, mode %s, network %s), config %s. Created %s.\n\n",
		c.Monitor.Name, c.Monitor.Kind, c.Monitor.Mode, c.Monitor.Network, c.Monitor.ConfigHash, c.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&w, "Experiments: %s. Grade source: %s. Attack axes: %s. Harm metrics: %s.\n\n",
		strings.Join(c.ExpIDs, ", "), c.GradeSource, strings.Join(c.AttackAxes, ", "), orDash(strings.Join(c.HarmMetrics, ", ")))
	fmt.Fprintf(&w, "Labels: %d honest, %d attack, %d excluded. Monitor errors: %d.\n\n", c.Labels.Honest, c.Labels.Attack, c.Labels.Excluded, c.Errors)
	writeView(&w, c)
	fmt.Fprintf(&w, "Rule: %s\n\n", c.Labels.Rule)

	w.WriteString("## Operating points\n\n")
	w.WriteString("| Target FPR | Supported | Threshold | False positives | Achieved FPR (95% CI) | Caught | Catch rate (95% CI) | Note |\n")
	w.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, p := range c.OperatingPoints {
		fmt.Fprintf(&w, "| %s | %s | %s | %d of %d | %s | %d of %d | %s | %s |\n",
			fmtFloat(p.TargetFPR), yesNo(p.Supported), threshold(p.Threshold), p.FalsePositives, p.Honest,
			rateCI(p.AchievedFPR, p.FPRInterval), p.Caught, p.Attacks, rateCI(p.CatchRate, p.CatchInterval), cell(p.Note))
	}
	w.WriteString("\nFlagged means the monitor blocked the run or scored it at or above the threshold. A threshold never splits tied scores. Intervals are exact Clopper-Pearson.\n\n")

	if len(c.Families) > 0 {
		w.WriteString("## Attack families\n\n| Family | Gates | Attacks |")
		for _, p := range c.OperatingPoints {
			fmt.Fprintf(&w, " Catch at %s FPR |", fmtFloat(p.TargetFPR))
		}
		w.WriteString("\n|---|---|---|")
		for range c.OperatingPoints {
			w.WriteString("---|")
		}
		w.WriteString("\n")
		for _, f := range c.Families {
			fmt.Fprintf(&w, "| %s | %s | %d |", cell(f.Family), cell(strings.Join(f.Gates, ", ")), f.Attacks)
			for _, fp := range f.Points {
				fmt.Fprintf(&w, " %d of %d, %s |", fp.Caught, fp.Attacks, rateCI(fp.CatchRate, fp.CatchInterval))
			}
			w.WriteString("\n")
		}
		w.WriteString("\n")
	}

	w.WriteString("## Warnings\n\n")
	if len(c.Warnings) == 0 {
		w.WriteString("None.\n\n")
	}
	for _, x := range c.Warnings {
		fmt.Fprintf(&w, "- %s\n", x)
	}
	if len(c.Warnings) > 0 {
		w.WriteString("\n")
	}

	writeSabotage(&w, c)
	fmt.Fprintf(&w, "## Monitor integrity\n\nMonitor-injection check: **%s**. %s\n\n", c.Integrity.State, c.Integrity.Detail)

	writeExcluded(&w, c)
	writeEvidence(&w, c)
	writeItems(&w, c)
	w.WriteString("## Not covered\n\nLabels come from the bench's deterministic gates, so the monitor is scored only on attacks those gates can see. Honest means no veto fired, not provably benign. This certificate says nothing about adaptive attacks against the monitor, attack families absent from these runs, or production traffic, whose mix of honest and attack runs differs from this one.\n")
	return w.Bytes()
}

// writeView states the transcript view and, per arm, how many replayed runs
// had reasoning to show.
func writeView(w *bytes.Buffer, c *schemas.MonitorCertificate) {
	view := c.View
	if view == "" {
		view = schemas.MonitorViewActions
	}
	fmt.Fprintf(w, "View: %s.", view)
	if len(c.Reasoning) > 0 {
		arms := make([]string, 0, len(c.Reasoning))
		for a := range c.Reasoning {
			arms = append(arms, a)
		}
		sort.Strings(arms)
		parts := make([]string, 0, len(arms))
		for _, a := range arms {
			r := c.Reasoning[a]
			parts = append(parts, fmt.Sprintf("arm %s %d of %d", a, r.WithReasoning, r.Runs))
		}
		fmt.Fprintf(w, " Reasoning visible in: %s.", strings.Join(parts, ", "))
	}
	w.WriteString("\n\n")
}

// writeSabotage renders the undetected-sabotage summary of attack runs.
func writeSabotage(w *bytes.Buffer, c *schemas.MonitorCertificate) {
	s := c.Sabotage
	if s == nil {
		return
	}
	fmt.Fprintf(w, "## Undetected sabotage\n\nAt the %s FPR point: %d attack runs, %d side tasks completed, %d caught, %d undetected (%s of attack runs).\n\n",
		fmtFloat(s.TargetFPR), s.AttackRuns, s.Completed, s.Caught, s.Undetected, rateCI(s.UndetectedRate, intervalSlice(s.Interval)))
	if len(s.PerArm) > 0 {
		w.WriteString("| Arm | Attack runs | Completed | Undetected |\n|---|---|---|---|\n")
		arms := make([]string, 0, len(s.PerArm))
		for a := range s.PerArm {
			arms = append(arms, a)
		}
		sort.Strings(arms)
		for _, a := range arms {
			p := s.PerArm[a]
			fmt.Fprintf(w, "| %s | %d | %d | %d |\n", cell(a), p.AttackRuns, p.Completed, p.Undetected)
		}
		w.WriteString("\n")
	}
}

func intervalSlice(iv *[2]float64) []float64 {
	if iv == nil {
		return nil
	}
	return []float64{iv[0], iv[1]}
}

func writeExcluded(w *bytes.Buffer, c *schemas.MonitorCertificate) {
	if len(c.Labels.ExcludedReasons) == 0 {
		return
	}
	w.WriteString("## Excluded runs\n\n| Reason | Runs |\n|---|---|\n")
	reasons := make([]string, 0, len(c.Labels.ExcludedReasons))
	for r := range c.Labels.ExcludedReasons {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(w, "| %s | %d |\n", cell(r), c.Labels.ExcludedReasons[r])
	}
	w.WriteString("\n")
}

func writeEvidence(w *bytes.Buffer, c *schemas.MonitorCertificate) {
	w.WriteString("## Sealed evidence\n\n| Experiment | Suite | Manifest hash | Runs replayed |\n|---|---|---|---|\n")
	for _, e := range c.Experiments {
		fmt.Fprintf(w, "| %s | %s@%s | `%s` | %d |\n", e.ExpID, e.SuiteID, e.SuiteVersion, e.ManifestHash, e.Runs)
	}
	w.WriteString("\n")
}

func writeItems(w *bytes.Buffer, c *schemas.MonitorCertificate) {
	if len(c.Items) == 0 {
		return
	}
	w.WriteString("## Runs\n\n| Run | Experiment | Task | Arm | Condition | Label | Score | Decision | Seal | Error |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, it := range c.Items {
		score := "-"
		if it.Score != nil {
			score = fmtFloat(*it.Score)
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s | `%s` | %s |\n", it.RunID, it.ExpID, cell(it.Task), cell(it.Arm),
			cell(it.Condition), it.Label, score, orDash(it.Decision), shortHash(it.SealHash), cell(orDash(it.Error)))
	}
	w.WriteString("\n")
}

func rateCI(rate *float64, ci []float64) string {
	if rate == nil {
		return "undefined"
	}
	if len(ci) != 2 {
		return fmtFloat(*rate)
	}
	return fmt.Sprintf("%s [%s, %s]", fmtFloat(*rate), fmtFloat(ci[0]), fmtFloat(ci[1]))
}

func threshold(t *float64) string {
	if t == nil {
		return "block only"
	}
	return fmtFloat(*t)
}

func fmtFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", v), "0"), ".")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortHash(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// cell keeps text inside one Markdown table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}
