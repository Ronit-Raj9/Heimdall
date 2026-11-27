package release

import (
	"math"

	"github.com/maximhq/bifrost/bench/schemas"
)

// comparisonSummary is the before and after of a release made from a
// comparison: the released arm and the other arm of the analysis, each with
// its raw pass rate over its counted runs (the analysis's naive scoreboard)
// and a 95% Wilson range. nil when the analysis has no count for the
// released arm.
func comparisonSummary(m *schemas.Manifest, a *schemas.Analysis, arm string) *schemas.ReleaseSummary {
	side := func(label string) (schemas.ReleaseSide, bool) {
		r, ok := a.Naive[label]
		if !ok || r.Den <= 0 {
			return schemas.ReleaseSide{}, false
		}
		s := schemas.ReleaseSide{Label: label, Runs: r.Den, Score: float64(r.Num) / float64(r.Den)}
		s.CILow, s.CIHigh = wilson95(r.Num, r.Den)
		if am, ok := m.Arms[label]; ok {
			s.Agent, s.Model = am.Config.ID, am.Config.Model
		}
		return s, true
	}
	cand, ok := side(arm)
	if !ok {
		return nil
	}
	out := &schemas.ReleaseSummary{Candidate: cand}
	base := ""
	if e := a.Verdict.Effect; e != nil && e.ArmA != "" && e.ArmA != arm {
		base = e.ArmA
	}
	if base == "" {
		for label := range a.Naive {
			if label != arm && (base == "" || label < base) {
				base = label
			}
		}
	}
	if b, ok := side(base); ok {
		out.Baseline = &b
	}
	return out
}

// wilson95 is the 95% Wilson score interval of x successes in n trials.
func wilson95(x, n int) (lo, hi float64) {
	const z = 1.959963984540054
	p, nn := float64(x)/float64(n), float64(n)
	d := 1 + z*z/nn
	c := (p + z*z/(2*nn)) / d
	h := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn)) / d
	return math.Max(0, c-h), math.Min(1, c+h)
}
