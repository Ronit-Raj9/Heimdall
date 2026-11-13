package reporters

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ReporterMarkdown names the Markdown reporter.
const ReporterMarkdown = "markdown"

// unknownText is how every value that could not be observed is written. It
// is never written as 0.
const unknownText = "unknown"

// markdownReporter writes report.md, a pull-request comment.
type markdownReporter struct{}

// newMarkdownReporter creates the Markdown reporter.
func newMarkdownReporter() *markdownReporter {
	return &markdownReporter{}
}

// GetName implements schemas.Part.
func (r *markdownReporter) GetName() string { return ReporterMarkdown }

// GetVersion implements schemas.Part.
func (r *markdownReporter) GetVersion() string { return Version }

// Write renders the report. The first line carries the decision and the
// action so a PR comment preview shows both. Numbers have fixed decimals and
// unknown values read "unknown". m may be nil; it only orders the arms.
func (r *markdownReporter) Write(ctx context.Context, a *schemas.Analysis, m *schemas.Manifest, w io.Writer) error {
	if a == nil {
		return errors.New("markdown report: analysis is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("markdown report: %w", err)
	}
	bw := bufio.NewWriter(w)
	arms := reportArms(a, m)
	writeHeader(bw, a)
	writePrimary(bw, &a.Verdict)
	writeGuards(bw, a.Verdict.Guards)
	writeInvalid(bw, a.Verdict.InvalidReasons)
	writeNaive(bw, a.Naive, arms)
	writeDiagnostics(bw, a.Diagnostics, arms)
	writeMeters(bw, a.Meters, arms)
	writeProvenance(bw, a)
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("markdown report for %s: %w", a.ExpID, err)
	}
	return nil
}

// writeHeader writes the decision line and the mode label.
func writeHeader(w *bufio.Writer, a *schemas.Analysis) {
	v := &a.Verdict
	fmt.Fprintf(w, "# Bifrost Bench: %s, action %s [%s]\n\n",
		strings.ToUpper(orUnknown(string(v.Decision))), strings.ToUpper(orUnknown(string(v.Action))), modeLabel(a.Mode))
	if a.Mode == schemas.ModeSynthetic {
		w.WriteString("> SYNTHETIC smoke test: scripted agent and scripted model. These numbers are not findings.\n\n")
	}
	fmt.Fprintf(w, "Experiment `%s`, analysis `%s`, grade source `%s`.\n\n",
		cell(a.ExpID), cell(a.AnalysisID), cell(orUnknown(a.GradeSource)))
}

// writePrimary writes the primary endpoint table.
func writePrimary(w *bufio.Writer, v *schemas.Verdict) {
	w.WriteString("## Primary endpoint\n\n| Field | Value |\n|---|---|\n")
	row(w, "Endpoint", v.Primary.Condition+" / "+v.Primary.Metric)
	row(w, "Decision", strings.ToUpper(orUnknown(string(v.Decision))))
	row(w, "d_bar", effectPP(v.Effect))
	row(w, "p-value", pValue(v.Effect))
	row(w, "Discordant tasks", discordant(v.Effect))
	mde := ppText(v.MDE, false)
	if mde != unknownText && v.MDEAssumedSD {
		mde += fmt.Sprintf(" (assumed SD %.2f)", v.SD)
	}
	row(w, "MDE", mde)
	row(w, "Alpha", fixed(v.Alpha, 3))
	if v.Effect != nil && v.Effect.LowerBound != nil && v.Effect.UpperBound != nil {
		row(w, "d_bar with unknowns against B / for B", effectPP(v.Effect.LowerBound)+" / "+effectPP(v.Effect.UpperBound))
	}
	if v.TasksNeeded > 0 {
		row(w, "Tasks needed to detect this d_bar", fmt.Sprintf("%d", v.TasksNeeded))
	} else if v.TasksNeededOver > 0 {
		row(w, "Tasks needed to detect this d_bar", fmt.Sprintf("more than %d", v.TasksNeededOver))
	}
	row(w, "Integrity", orUnknown(string(v.Integrity)))
	w.WriteString("\n")
}

// writeGuards writes one row per release guard.
func writeGuards(w *bufio.Writer, guards []schemas.GuardResult) {
	w.WriteString("## Guards\n\n")
	if len(guards) == 0 {
		w.WriteString("None.\n\n")
		return
	}
	w.WriteString("| Guard | Condition | Status | Effect | Detail |\n|---|---|---|---|---|\n")
	for _, g := range guards {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", cell(g.Spec.Rule), cell(orDash(g.Spec.Condition)),
			strings.ToUpper(orUnknown(string(g.Status))), effectPP(g.Effect), cell(orDash(g.Detail)))
	}
	w.WriteString("\n")
}

// writeInvalid lists why the experiment is invalid, if it is.
func writeInvalid(w *bufio.Writer, reasons []string) {
	w.WriteString("## Invalid reasons\n\n")
	if len(reasons) == 0 {
		w.WriteString("None.\n\n")
		return
	}
	for _, r := range reasons {
		fmt.Fprintf(w, "- %s\n", cell(r))
	}
	w.WriteString("\n")
}

// writeNaive writes the naive scoreboard, shown beside the verdict on purpose.
func writeNaive(w *bufio.Writer, naive map[string]schemas.Ratio, arms []string) {
	w.WriteString("## Naive scoreboard\n\n| Arm | Passes over runs |\n|---|---|\n")
	for _, arm := range arms {
		r, ok := naive[arm]
		text := unknownText
		if ok {
			text = ratio(r)
		}
		row(w, arm, text)
	}
	w.WriteString("\n")
}

// writeDiagnostics writes one row per condition.
func writeDiagnostics(w *bufio.Writer, rows []schemas.DiagnosticsRow, arms []string) {
	w.WriteString("## Diagnostics\n\n")
	if len(rows) == 0 {
		w.WriteString("None.\n\n")
		return
	}
	w.WriteString("| Condition | Axis | Tasks |")
	for _, arm := range arms {
		w.WriteString(" " + cell(arm) + " |")
	}
	w.WriteString(" Release effect |\n|---|---|---|" + strings.Repeat("---|", len(arms)) + "---|\n")
	for _, d := range rows {
		fmt.Fprintf(w, "| %s | %s | %d |", cell(d.Condition), cell(orDash(d.Axis)), d.NTasks)
		for _, arm := range arms {
			text := unknownText
			if r, ok := d.PerArm[arm]; ok {
				text = ratio(r)
			}
			w.WriteString(" " + text + " |")
		}
		effect := effectPP(d.Effect)
		if d.Descriptive && effect != unknownText {
			effect += " (descriptive)"
		}
		w.WriteString(" " + effect + " |\n")
	}
	w.WriteString("\n")
}

// writeMeters writes the always-on meters per arm.
func writeMeters(w *bufio.Writer, meters []schemas.Meter, arms []string) {
	w.WriteString("## Meters\n\n")
	if len(meters) == 0 {
		w.WriteString("None.\n\n")
		return
	}
	w.WriteString("| Meter | Definition |")
	for _, arm := range arms {
		w.WriteString(" " + cell(arm) + " |")
	}
	w.WriteString("\n|---|---|" + strings.Repeat("---|", len(arms)) + "\n")
	for _, mt := range meters {
		def := mt.Numerator
		if mt.Denominator != "" {
			def += " / " + mt.Denominator
		}
		fmt.Fprintf(w, "| %s | %s |", cell(orDash(mt.Label)), cell(orDash(def)))
		for _, arm := range arms {
			text := unknownText
			if v, ok := mt.PerArm[arm]; ok {
				text = valueText(v, mt.Kind)
			}
			w.WriteString(" " + text + " |")
		}
		w.WriteString("\n")
	}
	w.WriteString("\n")
}

// writeProvenance names the frozen manifest and how the verdict was reached.
func writeProvenance(w *bufio.Writer, a *schemas.Analysis) {
	v := &a.Verdict
	w.WriteString("## Provenance\n\n| Field | Value |\n|---|---|\n")
	row(w, "Manifest hash", "`"+cell(orUnknown(v.ManifestHash))+"`")
	row(w, "Frozen at", timeText(v.FrozenAt))
	row(w, "Analysis created", timeText(a.CreatedAt))
	row(w, "Randomization unit", orUnknown(v.RandomizationUnit))
	row(w, "Tie rule", orUnknown(v.TieRule))
	row(w, "Mode", modeLabel(a.Mode))
}

// reportArms orders the arms: the manifest's arms when known, otherwise every
// arm the analysis mentions, sorted.
func reportArms(a *schemas.Analysis, m *schemas.Manifest) []string {
	seen := map[string]bool{}
	if m != nil {
		for arm := range m.Arms {
			seen[arm] = true
		}
	}
	if len(seen) == 0 {
		for arm := range a.Naive {
			seen[arm] = true
		}
		for _, d := range a.Diagnostics {
			for arm := range d.PerArm {
				seen[arm] = true
			}
		}
		for _, mt := range a.Meters {
			for arm := range mt.PerArm {
				seen[arm] = true
			}
		}
	}
	arms := make([]string, 0, len(seen))
	for arm := range seen {
		arms = append(arms, arm)
	}
	slices.Sort(arms)
	return arms
}

// effectPP formats an effect's d_bar in signed percentage points.
func effectPP(e *schemas.Effect) string {
	if e == nil {
		return unknownText
	}
	return ppText(e.DBar*100, true)
}

// ppText formats percentage points with one decimal. NaN, infinities and,
// unless signed values are expected, non-positive values are unknown: an MDE
// of 0 pp cannot be observed.
func ppText(pp float64, signed bool) string {
	if math.IsNaN(pp) || math.IsInf(pp, 0) || (!signed && pp <= 0) {
		return unknownText
	}
	if signed {
		return fmt.Sprintf("%+.1f pp", pp)
	}
	return fmt.Sprintf("%.1f pp", pp)
}

// pValue formats the exact sign-flip p-value, unknown when there is none.
func pValue(e *schemas.Effect) string {
	if e == nil || e.PValue == nil {
		return unknownText
	}
	return fixed(*e.PValue, 4)
}

// discordant formats the discordant tasks over all tasks.
func discordant(e *schemas.Effect) string {
	if e == nil {
		return unknownText
	}
	return fmt.Sprintf("%d of %d", e.NNonzero, e.NTasks)
}

// ratio formats a count over its denominator with unknowns kept apart.
func ratio(r schemas.Ratio) string {
	switch {
	case r.Den == 0 && r.Unknown == 0:
		return "n/a"
	case r.Den == 0:
		return fmt.Sprintf("%s (%d)", unknownText, r.Unknown)
	case r.Unknown > 0:
		return fmt.Sprintf("%d/%d (+%d %s)", r.Num, r.Den, r.Unknown, unknownText)
	default:
		return fmt.Sprintf("%d/%d", r.Num, r.Den)
	}
}

// valueText formats a measured value by its kind with fixed decimals.
func valueText(v schemas.Value, kind schemas.ValueKind) string {
	if v.State == schemas.ValueNotApplicable {
		return "n/a"
	}
	f, ok := v.Float()
	if !ok {
		return unknownText
	}
	switch kind {
	case schemas.KindRate:
		return fmt.Sprintf("%.1f%%", f*100)
	case schemas.KindUSD:
		return fmt.Sprintf("$%.4f", f)
	case schemas.KindDuration:
		return fmt.Sprintf("%.1f s", f)
	case schemas.KindTokens:
		return fmt.Sprintf("%.0f", f)
	default:
		return fmt.Sprintf("%.1f", f)
	}
}

// fixed formats a number with the given decimals, unknown when not finite.
func fixed(f float64, decimals int) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return unknownText
	}
	return fmt.Sprintf("%.*f", decimals, f)
}

// timeText formats a time in UTC, unknown when unset.
func timeText(t time.Time) string {
	if t.IsZero() {
		return unknownText
	}
	return t.UTC().Format(time.RFC3339)
}

// modeLabel is the upper-case mode badge.
func modeLabel(m schemas.Mode) string {
	return strings.ToUpper(orUnknown(string(m)))
}

// row writes a two-column table row.
func row(w *bufio.Writer, field, value string) {
	fmt.Fprintf(w, "| %s | %s |\n", cell(field), value)
}

// cell makes text safe inside a Markdown table cell of a PR comment: no pipe
// breaks the table, no newline ends the row, and no HTML tag renders.
func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\r", " ", "\n", " ", "<", "&lt;", ">", "&gt;").Replace(s)
}

// orUnknown returns s, or "unknown" when it is empty.
func orUnknown(s string) string {
	if s == "" {
		return unknownText
	}
	return s
}

// orDash returns s, or "-" when it is empty and absence is not an unknown.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
