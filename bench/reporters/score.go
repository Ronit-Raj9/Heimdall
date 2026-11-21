package reporters

import (
	"bufio"
	"fmt"
	"math"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// writeScore writes the score table of a one-arm experiment: the arm's
// primary metric with its interval, and no comparison, p-value or MDE.
func writeScore(w *bufio.Writer, v *schemas.Verdict) {
	s := v.Score
	w.WriteString("## Score\n\nOne setup scored on its own: no comparison, so no release verdict.\n\n| Field | Value |\n|---|---|\n")
	arm := s.Arm
	if s.Label != "" {
		arm += " (" + s.Label + ")"
	}
	row(w, "Setup", cell(arm))
	row(w, "Endpoint", cell(s.Condition+" / "+s.Metric))
	row(w, "Decision", strings.ToUpper(orUnknown(string(v.Decision))))
	row(w, "Score", scoreValue(s.Metric, s.Mean))
	if s.Interval != nil {
		row(w, fmt.Sprintf("%.0f%% interval", 100*s.Conf), scoreValue(s.Metric, &s.Interval[0])+" to "+scoreValue(s.Metric, &s.Interval[1]))
	} else {
		row(w, fmt.Sprintf("%.0f%% interval", 100*s.Conf), unknownText)
	}
	row(w, "Interval method", cell(s.Method))
	if s.Low != nil && s.High != nil && s.Observed < s.Runs {
		row(w, "Score with unknown runs as fail / pass", scoreValue(s.Metric, s.Low)+" / "+scoreValue(s.Metric, s.High))
	}
	row(w, "Tasks", fmt.Sprintf("%d", s.Tasks))
	row(w, "Runs observed", fmt.Sprintf("%d of %d", s.Observed, s.Runs))
	row(w, "Integrity", orUnknown(string(v.Integrity)))
	w.WriteString("\n")
}

// scoreValue prints a rate as a percentage and any other metric in its unit.
func scoreValue(metric string, x *float64) string {
	if x == nil || math.IsNaN(*x) || math.IsInf(*x, 0) {
		return unknownText
	}
	if rateEndpoint(metric) {
		return fmt.Sprintf("%.1f%%", 100**x)
	}
	if *x == 0 {
		return "0"
	}
	return unitText(metric, *x, false)
}
