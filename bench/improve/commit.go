package improve

import (
	"fmt"
	"math"

	"github.com/maximhq/bifrost/bench/stats"
)

// LookRecord is one gate look whose test ran: its round, the level it was
// tested at and whether it committed. Record.Looks holds them in order.
type LookRecord struct {
	Round     int     `json:"round"`
	Level     float64 `json:"level"`
	Committed bool    `json:"committed"`
}

// fallbackSignFlip is the commit gate (A2.2): the one-sided exact sign-flip
// test on per-task mean differences, with the online fallback error budget
// over gate looks. Each of the L looks gets alpha_g / L; a look that
// committed passes its whole level on to the next look. The chance of
// committing any version that is not better than its parent is at most
// alpha_g whatever the dependence between looks.
type fallbackSignFlip struct{}

// missingShare is the share of gate tasks without a result on an arm above
// which the test does not run.
const missingShare = 0.2

// Level is the level of look len(looks)+1, or 0 when every look is spent.
func (fallbackSignFlip) Level(spec *Spec, looks []LookRecord) float64 {
	alpha := spec.Commit.Alpha
	n := spec.Commit.AuditLooks
	if n < 1 {
		n = max(1, spec.Rounds)
	}
	if len(looks) >= n {
		return 0
	}
	gamma := alpha / float64(n)
	level := gamma
	for k := 1; k <= len(looks); k++ {
		carry := 0.0
		if looks[k-1].Committed {
			carry = level
		}
		level = gamma + carry
	}
	return level
}

// Decide runs the gate test on per-task differences d (candidate minus
// incumbent), given missing gate tasks out of total.
func (fallbackSignFlip) Decide(d []float64, missing, total int, level float64) CommitDecision {
	out := CommitDecision{Level: level, N: len(d), Missing: missing, P: 1}
	if float64(missing) > missingShare*float64(total) {
		out.Skipped = true
		out.Why = fmt.Sprintf("the gate test did not run: %d of %d gate tasks had no result on one of the versions", missing, total)
		return out
	}
	out.Mean = mean(d)
	out.NNonzero = nonzero(d)
	if len(d) == 0 || stats.MinPossiblePUpper(out.NNonzero) > level {
		out.Why = "the two versions differed on too few gate tasks for any test to pass"
		return out
	}
	r := stats.SignFlipUpper(d)
	out.P = r.P
	out.Commit = r.P <= level && out.Mean > 0
	if out.Commit {
		out.Why = fmt.Sprintf("Kept: better on %d gate tasks by %s points (p %s, needed %s).", out.N, formatPoints(out.Mean), formatP(out.P), formatP(level))
	} else {
		out.Why = fmt.Sprintf("Not kept: %s points on %d gate tasks (p %s, needed %s).", formatPoints(out.Mean), out.N, formatP(out.P), formatP(level))
	}
	return out
}

func mean(d []float64) float64 {
	if len(d) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range d {
		s += v
	}
	return s / float64(len(d))
}

func nonzero(d []float64) int {
	n := 0
	for _, v := range d {
		if v != 0 {
			n++
		}
	}
	return n
}

// formatPoints writes a difference in [-1, 1] as points (100 x) with a sign:
// no decimals above 10 points, one decimal at or below (A2.12).
func formatPoints(x float64) string {
	p := 100 * x
	sign := ""
	if p > 0 {
		sign = "+"
	}
	if math.Abs(p) > 10 {
		return sign + fmt.Sprintf("%.0f", p)
	}
	if math.Abs(p) < 0.05 {
		return "0.0"
	}
	return sign + fmt.Sprintf("%.1f", p)
}

// formatP writes a p-value or level with two significant digits.
func formatP(p float64) string {
	if math.IsNaN(p) {
		return "not available"
	}
	if p <= 0 {
		return "0"
	}
	dec := max(0, 1-int(math.Floor(math.Log10(p))))
	return fmt.Sprintf("%.*f", dec, p)
}
