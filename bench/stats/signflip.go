package stats

import (
	"context"
	"math"
	"math/rand/v2"
)

// ExactMaxNonzero is the largest number of nonzero differences SignFlip
// enumerates exactly. 2^22 sign vectors take a few milliseconds with the
// split enumeration; every extra task doubles the cost, so above this the
// test switches to Monte Carlo.
const ExactMaxNonzero = 22

// MonteCarloDraws is how many random sign vectors SignFlip draws when it
// cannot enumerate. The Monte Carlo standard error is then at most 0.0005.
const MonteCarloDraws = 1_000_000

const (
	tieRelTol  = 1e-9  // fraction of sum |d_i|; |sum| this close to the observed one counts as a tie
	zeroRelTol = 1e-12 // fraction of max |d_i|; smaller differences are floating noise and count as zero
	mcSeedHi   = 0x9e3779b97f4a7c15
	mcSeedLo   = 0xbf58476d1ce4e5b9
)

// SignFlipResult is the outcome of SignFlip.
type SignFlipResult struct {
	P         float64 `json:"p"`          // two-sided p-value
	NNonzero  int     `json:"n_nonzero"`  // discordant tasks
	NPositive int     `json:"n_positive"` // tasks where d_i > 0
	NNegative int     `json:"n_negative"` // tasks where d_i < 0
	Exact     bool    `json:"exact"`      // true when every sign vector was enumerated
	Draws     int     `json:"draws"`      // random sign vectors drawn; 0 when exact
}

// SignFlip runs the exact two-sided sign-flip (paired permutation) test on
// per-task differences d (DESIGN.md 9.3). Under the null, swapping the arm
// labels of one task turns d_i into -d_i, so p is the fraction of the 2^n
// sign vectors s with |sum s_i*d_i| >= |sum d_i|.
//
// Ties: a sum within 1e-9 of sum |d_i| below the observed one counts as at
// least as extreme, so floating error never decides a tie. A difference
// within 1e-12 of the largest |d_i| counts as zero. Zero differences stay in
// n but cannot change any sum, so each one doubles both the count and the
// number of vectors; p is therefore computed over the nonzero ones only.
//
// With at most ExactMaxNonzero nonzero differences every vector is
// enumerated. Above that, MonteCarloDraws vectors are drawn from a fixed-seed
// PCG source, so the same input always gives the same p, and p is
// (hits+1)/(draws+1) so it is never zero and the test stays valid. Empty or
// all-zero input gives P=1. Any NaN or infinite difference gives P=NaN, which
// never passes a p < alpha check.
func SignFlip(d []float64) SignFlipResult {
	res, _ := SignFlipCtx(context.Background(), d)
	return res
}

// SignFlipCtx is SignFlip that stops when ctx ends, returning ctx's cause:
// a request computing many tests (a leaderboard's all-pair family) never runs
// past its deadline (audit qc2-05-09). The result is identical otherwise.
func SignFlipCtx(ctx context.Context, d []float64) (SignFlipResult, error) {
	res := SignFlipResult{P: 1, Exact: true}
	if err := ctx.Err(); err != nil {
		return res, context.Cause(ctx)
	}
	maxAbs := 0.0
	for _, v := range d {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			res.P = math.NaN()
			return res, nil
		}
		maxAbs = math.Max(maxAbs, math.Abs(v))
	}
	if maxAbs == 0 {
		return res, nil
	}
	nonzero := make([]float64, 0, len(d))
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
	threshold := math.Abs(observed) - tieRelTol*scale
	if threshold <= 0 {
		// Every sign vector is at least as extreme as a zero sum.
		return res, nil
	}
	var err error
	if len(nonzero) <= ExactMaxNonzero {
		res.P, err = exactSignFlip(ctx, nonzero, threshold)
		return res, err
	}
	res.Exact = false
	res.Draws = MonteCarloDraws
	res.P, err = monteCarloSignFlip(ctx, nonzero, threshold, MonteCarloDraws)
	return res, err
}

// MinPossibleP is the smallest p-value SignFlip can return with nNonzero
// discordant tasks: 2 * 0.5^nNonzero, capped at 1. Below 6 it is above 0.05,
// so no verdict can be directional however lopsided the split (DESIGN.md 9.3).
func MinPossibleP(nNonzero int) float64 {
	if nNonzero <= 1 {
		return 1
	}
	return math.Ldexp(1, 1-nNonzero)
}

// exactSignFlip enumerates all 2^n sign vectors of x by splitting x in two
// halves, so each partial sum adds at most 11 terms (small rounding error) and
// the total work is one addition per vector.
func exactSignFlip(ctx context.Context, x []float64, threshold float64) (float64, error) {
	half := len(x) / 2
	left := signedSums(x[:half])
	right := signedSums(x[half:])
	var hits uint64
	for i, a := range left {
		if i%256 == 0 && ctx.Err() != nil {
			return math.NaN(), context.Cause(ctx)
		}
		for _, b := range right {
			if math.Abs(a+b) >= threshold {
				hits++
			}
		}
	}
	return float64(hits) / float64(uint64(1)<<len(x)), nil
}

// signedSums returns sum_i s_i*x_i for every sign vector s, indexed by the
// bit mask of minus signs.
func signedSums(x []float64) []float64 {
	out := make([]float64, 1<<len(x))
	for mask := range out {
		s := 0.0
		for i, v := range x {
			if mask&(1<<i) != 0 {
				s -= v
			} else {
				s += v
			}
		}
		out[mask] = s
	}
	return out
}

// monteCarloSignFlip estimates the sign-flip p-value from random sign
// vectors, one random bit per difference.
func monteCarloSignFlip(ctx context.Context, x []float64, threshold float64, draws int) (float64, error) {
	r := rand.New(rand.NewPCG(mcSeedHi, mcSeedLo))
	sign := [2]float64{1, -1} // indexed by the random bit, so the loop has no branch to mispredict
	hits := 0
	for n := range draws {
		if n%4096 == 0 && ctx.Err() != nil {
			return math.NaN(), context.Cause(ctx)
		}
		s := 0.0
		var bits uint64
		for i, v := range x {
			if i%64 == 0 {
				bits = r.Uint64()
			}
			s += sign[bits&1] * v
			bits >>= 1
		}
		if math.Abs(s) >= threshold {
			hits++
		}
	}
	return float64(hits+1) / float64(draws+1), nil
}
