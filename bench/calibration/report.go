package calibration

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Judged reports whether an experiment is judged: it froze a judge, or a
// counted run carries a judge record. Apply leaves every other experiment
// untouched.
func Judged(m *schemas.Manifest, runs []schemas.SealedRun) bool { return judgedExperiment(m, runs) }

// Section renders the report's "Judge calibration" section, or "" when the
// analysis carries no calibration result.
func Section(jc *schemas.JudgeCalibrationVerdict) string {
	if jc == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Judge calibration\n\n")
	fmt.Fprintf(&b, "State: **%s**. %s.\n\n", jc.State, jc.Detail)
	if jc.CappedFrom != "" {
		fmt.Fprintf(&b, "The verdict was %s and is capped at INCONCLUSIVE: a judge that has not been shown to agree with people cannot decide a release.\n\n", strings.ToUpper(string(jc.CappedFrom)))
	}
	fmt.Fprintf(&b, "- Judge model: %s; judged runs: %d; contested runs: %d\n", jc.Model, jc.JudgedRuns, jc.ContestedRuns)
	if jc.RecordID != "" {
		at := ""
		if jc.RecordAt != nil {
			at = jc.RecordAt.Format("2006-01-02")
		}
		fmt.Fprintf(&b, "- Calibration record: %s (%s), %d human labels\n", jc.RecordID, at, jc.HumanLabels)
	}
	if jc.Agreement != nil {
		fmt.Fprintf(&b, "- Agreement %s; TNR %s; false-positive rate %s", rateText(*jc.Agreement), rateText(*jc.TNR), rateText(*jc.FPR))
		if jc.Kappa != nil {
			fmt.Fprintf(&b, "; Cohen's kappa %.2f", *jc.Kappa)
		}
		b.WriteString(" (exact 95% intervals)\n")
	}
	t := jc.Thresholds
	fmt.Fprintf(&b, "- Thresholds (%s): agreement lower bound >= %.2f, TNR lower bound >= %.2f, at least %d human labels, records older than %d days get a reminder\n",
		t.Source, t.MinAgreement, t.MinTNR, t.MinLabels, t.MaxAgeDays)
	if jc.ErrorOverEffect != "" {
		fmt.Fprintf(&b, "- **Judge error over effect**: %s\n", jc.ErrorOverEffect)
	}
	for _, r := range jc.Reminders {
		fmt.Fprintf(&b, "- Reminder: %s\n", r)
	}
	if len(jc.BiasAdjusted) > 0 {
		b.WriteString("\nJudged success rate per arm, raw and corrected for the judge's measured TPR and TNR (Rogan-Gladen; report only, never the verdict):\n\n")
		b.WriteString("| Arm | Raw judged rate | Corrected estimate | Corrected interval | Note |\n|---|---|---|---|---|\n")
		arms := make([]string, 0, len(jc.BiasAdjusted))
		for a := range jc.BiasAdjusted {
			arms = append(arms, a)
		}
		sort.Strings(arms)
		for _, a := range arms {
			r := jc.BiasAdjusted[a]
			est, iv := "n/a", "n/a"
			if r.Estimate != nil {
				est = fmt.Sprintf("%.3f", *r.Estimate)
			}
			if r.Lo != nil && r.Hi != nil {
				iv = fmt.Sprintf("[%.3f, %.3f]", *r.Lo, *r.Hi)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", a, rateText(r.Raw), est, iv, r.Note)
		}
	}
	return b.String()
}
