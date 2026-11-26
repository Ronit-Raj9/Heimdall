package improve

import (
	"math"

	"github.com/maximhq/bifrost/bench/stats"
)

// Defaults of the claim on the held-out tasks (A2.5, D11).
const (
	DefaultClaimAlpha     = 0.05
	DefaultExtensionAlpha = 0.02
	DefaultTargetGain     = 0.05
	// LikelyBetterP is the one-sided p at or below which a positive gain that
	// is not proven reads "likely better".
	LikelyBetterP = 0.20
	// claimSDFloor keeps the detectable gain honest when the differences
	// happen to agree.
	claimSDFloor = 0.10
)

// signFlipClaim is the claim rule (A2.5): the one-sided exact sign-flip test
// of the final experiment's per-task mean differences, final minus version
// 0, at a_main, with approximate paired-t intervals and the detectable gain
// after the fact.
type signFlipClaim struct{}

// mainLevel is a_main: the claim alpha minus the reserve for the second look
// when the extension is on. An old record with no claim block reads at the
// commit alpha.
func mainLevel(s *Spec) float64 {
	if s == nil {
		return DefaultClaimAlpha
	}
	c := s.Claim
	if c.Alpha == 0 {
		if s.Commit.Alpha > 0 {
			return s.Commit.Alpha
		}
		return DefaultClaimAlpha
	}
	if c.Extension {
		return c.Alpha - c.ExtensionAlpha
	}
	return c.Alpha
}

// Decide computes every number of the verdict and its state, then calls
// Wording when set.
func (signFlipClaim) Decide(in ClaimInput) *Verdict {
	level := in.Level
	if level <= 0 {
		level = mainLevel(in.Spec)
	}
	target := DefaultTargetGain
	if in.Spec != nil && in.Spec.Claim.TargetGain > 0 {
		target = in.Spec.Claim.TargetGain
	}
	d := TaskDiffs(in.A, in.B, in.Tasks)
	v := &Verdict{Level: level, N: len(d), NNonzero: nonzero(d), Gain: mean(d)}
	v.PUpper = stats.SignFlipUpper(d).P
	neg := make([]float64, len(d))
	for i, x := range d {
		neg[i] = -x
	}
	v.PLower = stats.SignFlipUpper(neg).P
	if lo, hi, ok := stats.PairedT(d, 0.80); ok {
		v.CI80 = [2]float64{lo, hi}
	}
	if lo, hi, ok := stats.PairedT(d, 0.95); ok {
		v.CI95 = [2]float64{lo, hi}
	}
	mde := math.Inf(1)
	if n := len(d); n >= 2 {
		sd := math.Max(stats.SD(d), claimSDFloor)
		mde = (stats.TQuantile(1-level, n-1) + stats.TQuantile(0.80, n-1)) * sd / math.Sqrt(float64(n))
	}
	if !math.IsInf(mde, 0) && !math.IsNaN(mde) {
		v.MDE = mde // 0 when it cannot be computed (fewer than 2 tasks)
	}
	v.SealedGain = v.Gain
	p := v.PUpper
	v.P = &p
	switch {
	case !in.Committed:
		v.State = VerdictNoChange
	case in.Approved:
		v.State = VerdictNotClaimable
	case v.PUpper <= level && v.Gain > 0:
		v.State = VerdictSupported
	case v.PLower <= level && v.Gain < 0:
		v.State = VerdictWorse
	case stats.MinPossiblePUpper(v.NNonzero) > level || mde > 2*target:
		v.State = VerdictInsufficientSample
	case v.Gain > 0 && v.PUpper <= LikelyBetterP:
		v.State = VerdictLikelyBetter
	default:
		v.State = VerdictNoEvidence
	}
	if Wording != nil {
		Wording(v, in.Rec)
	}
	return v
}
