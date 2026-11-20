package livescore

import "testing"

// Regression for 8-F-02: only a whole, plain decimal token in [0, 1] is a score.
func TestParseScoreRejectsInvalidTokens(t *testing.T) {
	ok := map[string]float64{
		"SCORE: 0.5":           0.5,
		"score=1":              1,
		"SCORE: 0":             0,
		"SCORE: .25.":          0.25,
		"SCORE: 1.0\n":         1,
		"x\nSCORE: 0.75, done": 0.75,
	}
	for in, want := range ok {
		got, err := parseScore(in)
		if err != nil || got != want {
			t.Errorf("parseScore(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"SCORE: 10", "SCORE: 1e-2", "SCORE: -0.5", "SCORE: +1", "SCORE: 1.5", "SCORE: 0.5abc", "SCORE: 2", "no score"} {
		if v, err := parseScore(in); err == nil {
			t.Errorf("parseScore(%q) accepted %v", in, v)
		}
	}
}
