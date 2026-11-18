package calibration

import (
	"fmt"
	"math"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Confidence is the two-sided level of every interval this package reports.
const Confidence = 0.95

// Rate is k of n with its exact two-sided 95% Clopper-Pearson interval.
func Rate(k, n int) schemas.CalibrationRate {
	return rateAt(k, n, Confidence)
}

// rateAt is Rate at another two-sided level.
func rateAt(k, n int, conf float64) schemas.CalibrationRate {
	r := schemas.CalibrationRate{K: k, N: n}
	if n <= 0 || k < 0 || k > n {
		return r
	}
	lo, hi := ClopperPearson(k, n, conf)
	v := float64(k) / float64(n)
	r.Value, r.Lo, r.Hi = &v, &lo, &hi
	return r
}

// ClopperPearson is the exact two-sided interval at level conf for k
// successes in n trials: the lower end is 0 when k is 0 and the upper end 1
// when k is n. The lower end mirrors the upper end of the failures:
// lo(k, n) = 1 - hi(n-k, n).
func ClopperPearson(k, n int, conf float64) (lo, hi float64) {
	one := 1 - (1-conf)/2
	hi = stats.ClopperPearsonUpper(k, n, one)
	lo = 0
	if k > 0 {
		lo = 1 - stats.ClopperPearsonUpper(n-k, n, one)
	}
	return lo, hi
}

// Majority is the verdict of a set of votes: correct or incorrect when more
// than half the votes say so, undecided otherwise (unparsable votes or a
// tie), the same rule the judge grader applies.
func Majority(votes []string) string {
	c, i := 0, 0
	for _, v := range votes {
		switch v {
		case schemas.CalibrationVerdictCorrect:
			c++
		case schemas.CalibrationVerdictIncorrect:
			i++
		}
	}
	switch {
	case 2*c > len(votes):
		return schemas.CalibrationVerdictCorrect
	case 2*i > len(votes):
		return schemas.CalibrationVerdictIncorrect
	}
	return schemas.CalibrationVerdictUndecided
}

// Compare computes the statistics of judge verdicts against human labels,
// item by item (labels[i] is the label of the item verdicts[i] judged).
// Positive is "correct". An undecided verdict is neither a positive nor a
// negative call: it counts against agreement, TPR and TNR and is never a
// false positive or a false negative.
//
// Cohen's kappa treats the judge as a rater with three categories (correct,
// incorrect, undecided) and the person with two: p_o is the agreement and
// p_e = P(label correct) P(judge correct) + P(label incorrect) P(judge
// incorrect). It is nil when p_e is 1 (every item in one category on both
// sides), where kappa is not defined.
func Compare(labels, verdicts []string) schemas.CalibrationStats {
	var s schemas.CalibrationStats
	judgeC, judgeI := 0, 0
	for i := range labels {
		if i >= len(verdicts) {
			break
		}
		s.Items++
		pos := labels[i] == schemas.CalibrationCorrect
		if pos {
			s.Positives++
		} else {
			s.Negatives++
		}
		switch verdicts[i] {
		case schemas.CalibrationVerdictCorrect:
			judgeC++
			if pos {
				s.TP++
			} else {
				s.FP++
			}
		case schemas.CalibrationVerdictIncorrect:
			judgeI++
			if pos {
				s.FN++
			} else {
				s.TN++
			}
		default:
			s.Undecided++
		}
	}
	s.Agreement = Rate(s.TP+s.TN, s.Items)
	s.TPR = Rate(s.TP, s.Positives)
	s.TNR = Rate(s.TN, s.Negatives)
	s.FPR = Rate(s.FP, s.Negatives)
	if s.Items > 0 {
		n := float64(s.Items)
		po := float64(s.TP+s.TN) / n
		pe := float64(s.Positives)/n*float64(judgeC)/n + float64(s.Negatives)/n*float64(judgeI)/n
		if pe < 1-1e-12 {
			k := (po - pe) / (1 - pe)
			s.Kappa = &k
		}
	}
	return s
}

// ByCategory computes the statistics per answer category, categories
// sorted; items without a category are under "(none)".
func ByCategory(categories, labels, verdicts []string) []schemas.CalibrationCategory {
	type acc struct{ labels, verdicts []string }
	m := map[string]*acc{}
	for i := range labels {
		c := categories[i]
		if c == "" {
			c = "(none)"
		}
		a, ok := m[c]
		if !ok {
			a = &acc{}
			m[c] = a
		}
		a.labels = append(a.labels, labels[i])
		a.verdicts = append(a.verdicts, verdicts[i])
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]schemas.CalibrationCategory, 0, len(keys))
	for _, k := range keys {
		out = append(out, schemas.CalibrationCategory{Category: k, Stats: Compare(m[k].labels, m[k].verdicts)})
	}
	return out
}

// Consistency is the judge's stability on the same inputs: with more than
// one vote, the share of items whose votes were all the same; and the share
// of items whose majority verdict was the same in the first and the second
// pass. Undecided counts as a verdict like any other.
func Consistency(votes int, firstVotes [][]string, first, second []string) schemas.CalibrationConsistency {
	c := schemas.CalibrationConsistency{Votes: votes}
	if votes > 1 {
		k := 0
		for _, vs := range firstVotes {
			if unanimous(vs) {
				k++
			}
		}
		c.UnanimousItems = Rate(k, len(firstVotes))
	}
	same := 0
	n := min(len(first), len(second))
	for i := 0; i < n; i++ {
		if first[i] == second[i] {
			same++
		}
	}
	c.RepeatAgreement = Rate(same, n)
	return c
}

func unanimous(vs []string) bool {
	for _, v := range vs {
		if v != vs[0] {
			return false
		}
	}
	return len(vs) > 0
}

// Flips is how many items' verdict changed between the first pass and a
// perturbed pass.
func Flips(name string, first, perturbed []string) schemas.CalibrationPerturbation {
	k := 0
	n := min(len(first), len(perturbed))
	for i := 0; i < n; i++ {
		if first[i] != perturbed[i] {
			k++
		}
	}
	return schemas.CalibrationPerturbation{Name: name, Flips: Rate(k, n)}
}

// RoganGladen corrects an observed judged success rate (k of n runs the
// judge passed) for the judge's measured sensitivity (TPR, tp of p) and
// specificity (TNR, tn of q): pi = (p_obs + TNR - 1) / (TPR + TNR - 1),
// clipped to [0, 1]. It is defined only when TPR + TNR > 1; at TPR = TNR =
// 1 it is p_obs (the identity).
//
// The interval is conservative: each of the three inputs gets its exact
// interval at 1 - 0.05/3 (Bonferroni, so all three hold together with at
// least 95%), and the estimate is evaluated at every corner of that box.
// The estimate is monotone in each input separately, so its extremes over
// the box are at corners. When some corner has TPR + TNR <= 1 the interval
// is not defined and the note says so.
func RoganGladen(k, n, tp, p, tn, q int) schemas.BiasAdjustedRate {
	out := schemas.BiasAdjustedRate{Raw: Rate(k, n)}
	switch {
	case n == 0:
		out.Note = "no judged run to correct"
		return out
	case p == 0 || q == 0:
		out.Note = "the calibration has no human label of one class, so TPR or TNR is not measured"
		return out
	}
	obs := float64(k) / float64(n)
	sens := float64(tp) / float64(p)
	spec := float64(tn) / float64(q)
	est, ok := rg(obs, sens, spec)
	if !ok {
		out.Note = fmt.Sprintf("not defined: TPR %.3f + TNR %.3f is not above 1, so the judge carries no information to correct with", sens, spec)
		return out
	}
	out.Estimate = &est
	conf := 1 - (1-Confidence)/3
	oLo, oHi := ClopperPearson(k, n, conf)
	sLo, sHi := ClopperPearson(tp, p, conf)
	cLo, cHi := ClopperPearson(tn, q, conf)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, o := range []float64{oLo, oHi} {
		for _, s := range []float64{sLo, sHi} {
			for _, c := range []float64{cLo, cHi} {
				v, ok := rg(o, s, c)
				if !ok {
					out.Note = "interval not defined: within the calibration's uncertainty TPR + TNR could be 1 or less; label more items"
					return out
				}
				lo, hi = math.Min(lo, v), math.Max(hi, v)
			}
		}
	}
	out.Lo, out.Hi = &lo, &hi
	out.Note = "report only: corrected for the judge's measured TPR and TNR; never replaces the verdict"
	return out
}

// rg is the Rogan-Gladen estimate clipped to [0, 1], false when TPR + TNR
// is not above 1.
func rg(obs, sens, spec float64) (float64, bool) {
	den := sens + spec - 1
	if den <= 1e-12 {
		return 0, false
	}
	v := (obs + spec - 1) / den
	return math.Min(1, math.Max(0, v)), true
}
