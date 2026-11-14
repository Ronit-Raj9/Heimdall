package verdict

import "github.com/maximhq/bifrost/bench/schemas"

// ReleaseEffect returns the release effect of armB over armA on one condition
// and metric, computed exactly as the verdict computes its primary endpoint and
// its no_regression guards: counted runs only, per-task paired differences,
// the exact sign-flip p-value, and both missingness bounds attached. The
// release policy reads it so a policy rule and a guard can never disagree on
// the same runs.
func ReleaseEffect(runs []schemas.SealedRun, condition, metric, armA, armB string) *schemas.Effect {
	idx := newRunIndex(runs)
	return pairedEffect(condition, metric, armA, armB, idx.cells(condition, armA, metric), idx.cells(condition, armB, metric), true)
}

// EffectDecision applies the directional part of DESIGN.md 9.3 item 6 to one
// effect: REGRESSED when it is significantly negative at alpha at both
// missingness bounds, IMPROVED when significantly positive at both, and
// INCONCLUSIVE otherwise. It never returns INVALID; instrument gates are the
// verdict's business.
func EffectDecision(e *schemas.Effect, alpha float64) schemas.Decision {
	switch {
	case regressedAtBoth(e, alpha):
		return schemas.DecisionRegressed
	case improvedAtBoth(e, alpha):
		return schemas.DecisionImproved
	default:
		return schemas.DecisionInconclusive
	}
}

// Direction is the sign of one effect when it is significant at alpha: +1
// for a significant positive d_bar, -1 for a significant negative one, 0
// otherwise. It reads the effect it is given, not its bounds, so a caller can
// ask about each missingness bound on its own.
func Direction(e *schemas.Effect, alpha float64) int {
	switch {
	case significant(e, alpha, 1):
		return 1
	case significant(e, alpha, -1):
		return -1
	default:
		return 0
	}
}

// Vetoed reports whether a veto gate fired in the run, reading the outcome too
// so a run sealed without its gate list still counts. It is the same test the
// integrity guard uses.
func Vetoed(r schemas.SealedRun) bool {
	return vetoed(r)
}
