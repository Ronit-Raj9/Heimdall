package verdict

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

func ones(n int) []float64 {
	d := make([]float64, n)
	for i := range d {
		d[i] = 1
	}
	return d
}

// Above stats.ExactMaxNonzero the p-value is sampled; the effect says so
// with its draw count, and an exact one carries none (audit 1-B-07).
func TestEffectCarriesSampledPDraws(t *testing.T) {
	var e schemas.Effect
	fillEffectStats(&e, ones(23), true)
	if e.PDraws != stats.MonteCarloDraws {
		t.Fatalf("23 tasks: draws %d", e.PDraws)
	}
	e = schemas.Effect{}
	fillEffectStats(&e, ones(8), true)
	if e.PDraws != 0 {
		t.Fatalf("8 tasks: draws %d, want exact", e.PDraws)
	}
}

// A sampled best case is lowered before it can prove futility; an exact one
// is used as is (audit 1-B-07).
func TestBestCasePSampledIsLowerBound(t *testing.T) {
	d := append(ones(12), make([]float64, 0)...)
	for range 11 {
		d = append(d, -1)
	}
	sampled := stats.SignFlip(append(append([]float64{}, d...), 1))
	if sampled.Exact {
		t.Fatal("want a sampled case")
	}
	if got := BestCaseP(d, 1); got >= sampled.P {
		t.Fatalf("best case %v not below sampled %v", got, sampled.P)
	}
	if got, want := BestCaseP(ones(5), 1), stats.SignFlip(ones(6)).P; got != want {
		t.Fatalf("exact best case %v, want %v", got, want)
	}
}
