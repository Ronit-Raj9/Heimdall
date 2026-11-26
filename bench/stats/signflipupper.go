package stats

import (
	"math"
	"math/rand/v2"
)

// UpperDraws is the Monte Carlo size of SignFlipUpper (standard error at
// most 0.0005 near p = 0.05).
const UpperDraws = 200_000

// SignFlipUpperDraws is the exported test hook for signFlipUpperDraws: the
// simulation suite of improvement runs calls it with a small draw count.
var SignFlipUpperDraws = signFlipUpperDraws

// SignFlipUpper is the one-sided exact sign-flip test of "B is better than
// A" on per-task differences d: p is the share of the 2^n sign vectors s with
// sum s_i d_i >= sum d_i (ties within tieRelTol of sum |d_i| count as at
// least as extreme). Zero differences are dropped (they never change a sum).
// Exact up to ExactMaxNonzero nonzero values, else UpperDraws random vectors
// from the fixed-seed source with p = (hits+1)/(draws+1). Empty or all-zero
// input gives P = 1; any NaN or Inf gives P = NaN. The P field of the result
// is the one-sided p-value.
func SignFlipUpper(d []float64) SignFlipResult {
	res := SignFlipResult{P: 1, Exact: true}
	nonzero, threshold, ok := upperPrep(d, &res)
	if !ok {
		return res
	}
	if len(nonzero) <= ExactMaxNonzero {
		res.P = exactUpper(nonzero, threshold)
		return res
	}
	res.Exact = false
	res.Draws = UpperDraws
	res.P = monteCarloUpper(nonzero, threshold, UpperDraws)
	return res
}

// signFlipUpperDraws is SignFlipUpper with a given Monte Carlo size and no
// exact path, for the simulation tests.
func signFlipUpperDraws(d []float64, draws int) float64 {
	res := SignFlipResult{P: 1}
	nonzero, threshold, ok := upperPrep(d, &res)
	if !ok {
		return res.P
	}
	if draws < 1 {
		draws = 1
	}
	return monteCarloUpper(nonzero, threshold, draws)
}

// MinPossiblePUpper is 0.5^nNonzero: the smallest one-sided p SignFlipUpper
// can return.
func MinPossiblePUpper(nNonzero int) float64 {
	if nNonzero <= 0 {
		return 1
	}
	return math.Ldexp(1, -nNonzero)
}

// upperPrep checks d, drops zeros and fills the counts of res. ok is false
// when res.P is already the answer (NaN, empty or all zero).
func upperPrep(d []float64, res *SignFlipResult) (nonzero []float64, threshold float64, ok bool) {
	maxAbs := 0.0
	for _, v := range d {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			res.P = math.NaN()
			return nil, 0, false
		}
		maxAbs = math.Max(maxAbs, math.Abs(v))
	}
	if maxAbs == 0 {
		return nil, 0, false
	}
	nonzero = make([]float64, 0, len(d))
	observed, scale := 0.0, 0.0
	for _, v := range d {
		if math.Abs(v) <= zeroRelTol*maxAbs {
			continue
		}
		nonzero = append(nonzero, v)
		observed += v
		scale += math.Abs(v)
		if v > 0 {
			res.NPositive++
		} else {
			res.NNegative++
		}
	}
	res.NNonzero = len(nonzero)
	// observed may be negative; the test is still computed (every result is valid).
	return nonzero, observed - tieRelTol*scale, true
}

// exactUpper counts sign vectors whose sum is at least threshold, with the
// split enumeration of exactSignFlip.
func exactUpper(x []float64, threshold float64) float64 {
	half := len(x) / 2
	left := signedSums(x[:half])
	right := signedSums(x[half:])
	var hits uint64
	for _, a := range left {
		for _, b := range right {
			if a+b >= threshold {
				hits++
			}
		}
	}
	return float64(hits) / float64(uint64(1)<<len(x))
}

// monteCarloUpper estimates the one-sided p-value from random sign vectors
// drawn from the same fixed-seed source as monteCarloSignFlip.
func monteCarloUpper(x []float64, threshold float64, draws int) float64 {
	r := rand.New(rand.NewPCG(mcSeedHi, mcSeedLo))
	sign := [2]float64{1, -1}
	hits := 0
	for range draws {
		s := 0.0
		var bits uint64
		for i, v := range x {
			if i%64 == 0 {
				bits = r.Uint64()
			}
			s += sign[bits&1] * v
			bits >>= 1
		}
		if s >= threshold {
			hits++
		}
	}
	return float64(hits+1) / float64(draws+1)
}
