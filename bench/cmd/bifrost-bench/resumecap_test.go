package main

import (
	"math"
	"strings"
	"testing"
)

// TestCheckResumeCap: run -total-usd is accepted only with -resume and only
// as a positive finite amount (live lane l1 test 10).
func TestCheckResumeCap(t *testing.T) {
	for _, c := range []struct {
		resume string
		usd    float64
		want   string
	}{
		{"", 0, ""},
		{"e_x", 0, ""},
		{"e_x", 2.5, ""},
		{"", 2.5, "needs -resume"},
		{"e_x", -1, "positive"},
		{"e_x", math.Inf(1), "positive"},
		{"e_x", math.NaN(), "positive"},
	} {
		err := checkResumeCap(c.resume, c.usd)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("checkResumeCap(%q, %v) = %v, want %q", c.resume, c.usd, err, c.want)
		}
	}
}
