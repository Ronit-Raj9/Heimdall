package calibration

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	c  = schemas.CalibrationVerdictCorrect
	i  = schemas.CalibrationVerdictIncorrect
	u  = schemas.CalibrationVerdictUndecided
	lc = schemas.CalibrationCorrect
	li = schemas.CalibrationIncorrect
)

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.6f, want %.6f", what, got, want)
	}
}

// Exact intervals against closed forms: for k = 0 the upper end is
// 1 - (alpha/2)^(1/n), for k = n the lower end is (alpha/2)^(1/n), and 5 of
// 10 is the textbook [0.1871, 0.8129].
func TestClopperPearsonKnownValues(t *testing.T) {
	edge := math.Pow(0.025, 0.1)
	lo, hi := ClopperPearson(0, 10, 0.95)
	near(t, "lo(0,10)", lo, 0, 0)
	near(t, "hi(0,10)", hi, 1-edge, 1e-9)
	lo, hi = ClopperPearson(10, 10, 0.95)
	near(t, "lo(10,10)", lo, edge, 1e-9)
	near(t, "hi(10,10)", hi, 1, 0)
	lo, hi = ClopperPearson(5, 10, 0.95)
	near(t, "lo(5,10)", lo, 0.187086, 1e-5)
	near(t, "hi(5,10)", hi, 0.812914, 1e-5)
	if r := Rate(0, 0); r.Value != nil || r.Lo != nil || r.Hi != nil {
		t.Fatalf("Rate(0,0) = %+v, want no value", r)
	}
}

func repeat(v string, n int) []string {
	out := make([]string, n)
	for k := range out {
		out[k] = v
	}
	return out
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// 50 items labelled correct (40 judged correct, 8 incorrect, 2 undecided)
// and 50 labelled incorrect (5 judged correct, 43 incorrect, 2 undecided).
// By hand: agreement 83/100, TPR 40/50, TNR 43/50, FPR 5/50; the judge says
// correct 45 times and incorrect 51 times, so p_e = 0.5*0.45 + 0.5*0.51 =
// 0.48 and kappa = (0.83 - 0.48) / 0.52 = 0.673077.
func TestCompareHandComputed(t *testing.T) {
	labels := concat(repeat(lc, 50), repeat(li, 50))
	verdicts := concat(repeat(c, 40), repeat(i, 8), repeat(u, 2), repeat(c, 5), repeat(i, 43), repeat(u, 2))
	s := Compare(labels, verdicts)
	if s.Items != 100 || s.Positives != 50 || s.Negatives != 50 || s.TP != 40 || s.FN != 8 || s.FP != 5 || s.TN != 43 || s.Undecided != 4 {
		t.Fatalf("counts %+v", s)
	}
	near(t, "agreement", *s.Agreement.Value, 0.83, 1e-12)
	near(t, "tpr", *s.TPR.Value, 0.80, 1e-12)
	near(t, "tnr", *s.TNR.Value, 0.86, 1e-12)
	near(t, "fpr", *s.FPR.Value, 0.10, 1e-12)
	near(t, "kappa", *s.Kappa, 0.35/0.52, 1e-12)
	lo, hi := ClopperPearson(83, 100, 0.95)
	near(t, "agreement lo", *s.Agreement.Lo, lo, 0)
	near(t, "agreement hi", *s.Agreement.Hi, hi, 0)
	if *s.Agreement.Lo > 0.83 || *s.Agreement.Hi < 0.83 {
		t.Fatalf("interval %v %v does not hold the estimate", *s.Agreement.Lo, *s.Agreement.Hi)
	}
}

func TestKappaUndefinedAndPerfect(t *testing.T) {
	if s := Compare(repeat(lc, 5), repeat(c, 5)); s.Kappa != nil {
		t.Fatalf("kappa with p_e = 1 is %v, want nil", *s.Kappa)
	}
	s := Compare([]string{lc, lc, li, li}, []string{c, c, i, i})
	near(t, "perfect kappa", *s.Kappa, 1, 1e-12)
	// The judge always says correct: agreement 0.5, p_e 0.5, kappa 0.
	s = Compare([]string{lc, li}, []string{c, c})
	near(t, "chance kappa", *s.Kappa, 0, 1e-12)
}

func TestMajority(t *testing.T) {
	for _, tt := range []struct {
		votes []string
		want  string
	}{
		{[]string{c}, c}, {[]string{"incorrect"}, i}, {[]string{"unparsable"}, u},
		{[]string{c, i, c}, c}, {[]string{c, i, "unparsable"}, u}, {[]string{i, i, c}, i}, {nil, u},
	} {
		if got := Majority(tt.votes); got != tt.want {
			t.Fatalf("Majority(%v) = %s, want %s", tt.votes, got, tt.want)
		}
	}
}

func TestConsistencyAndFlips(t *testing.T) {
	cons := Consistency(3, [][]string{{c, c, c}, {c, i, c}, {i, i, i}}, []string{c, c, i}, []string{c, i, i})
	if cons.UnanimousItems.K != 2 || cons.UnanimousItems.N != 3 || cons.RepeatAgreement.K != 2 || cons.RepeatAgreement.N != 3 {
		t.Fatalf("consistency %+v", cons)
	}
	if one := Consistency(1, [][]string{{c}}, []string{c}, []string{c}); one.UnanimousItems.N != 0 {
		t.Fatalf("one vote has no unanimity measure: %+v", one)
	}
	f := Flips(PerturbPreamble, []string{c, i, c, c}, []string{c, c, c, i})
	if f.Name != PerturbPreamble || f.Flips.K != 2 || f.Flips.N != 4 {
		t.Fatalf("flips %+v", f)
	}
}

func TestByCategory(t *testing.T) {
	out := ByCategory([]string{"b", "a", "", "a"}, []string{lc, li, lc, lc}, []string{c, i, i, c})
	if len(out) != 3 || out[0].Category != "(none)" || out[1].Category != "a" || out[2].Category != "b" {
		t.Fatalf("categories %+v", out)
	}
	if a := out[1].Stats; a.Items != 2 || a.TP != 1 || a.TN != 1 {
		t.Fatalf("category a %+v", a)
	}
}

// Rogan-Gladen: with TPR = TNR = 1 the estimate is the raw rate (the
// identity); by hand, 60 of 100 judged passes with TPR 0.9 and TNR 0.8 give
// (0.6 + 0.8 - 1) / (0.9 + 0.8 - 1) = 0.4 / 0.7.
func TestRoganGladen(t *testing.T) {
	id := RoganGladen(37, 50, 40, 40, 30, 30)
	near(t, "identity", *id.Estimate, 37.0/50, 1e-12)
	if *id.Lo > *id.Estimate || *id.Hi < *id.Estimate {
		t.Fatalf("identity interval %v..%v", *id.Lo, *id.Hi)
	}
	r := RoganGladen(60, 100, 90, 100, 80, 100)
	near(t, "estimate", *r.Estimate, 0.4/0.7, 1e-12)
	if r.Lo == nil || r.Hi == nil || *r.Lo > *r.Estimate || *r.Hi < *r.Estimate || *r.Lo < 0 || *r.Hi > 1 {
		t.Fatalf("interval %+v", r)
	}
	if r.Raw.K != 60 || r.Raw.N != 100 {
		t.Fatalf("raw %+v", r.Raw)
	}
	none := RoganGladen(10, 20, 5, 10, 5, 10)
	if none.Estimate != nil || none.Note == "" {
		t.Fatalf("TPR + TNR = 1 must be undefined: %+v", none)
	}
	wide := RoganGladen(10, 20, 4, 5, 4, 5)
	if wide.Estimate == nil || wide.Lo != nil || wide.Note == "" {
		t.Fatalf("a small calibration must leave the interval undefined: %+v", wide)
	}
	if empty := RoganGladen(0, 0, 1, 1, 1, 1); empty.Estimate != nil {
		t.Fatalf("no runs: %+v", empty)
	}
}
