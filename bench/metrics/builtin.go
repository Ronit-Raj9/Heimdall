// Package metrics holds the built-in per-run metrics (docs/DESIGN.md 9.5).
//
// Each metric reads only the run context and the collected evidence and
// returns one schemas.Value. A number that could not be observed is unknown
// with a reason, never zero, so missing gateway rows or an undecided grader
// cannot pass for a real result. Aggregation over runs and arms happens
// elsewhere; the descriptor states the denominator each mean uses.
package metrics

import "github.com/maximhq/bifrost/bench/schemas"

// Builtin returns the metrics that ship with the bench, in catalog order: the
// primary endpoint first, then the PS2 metrics, then the diagnostics. Each
// call builds new instances so callers never share state.
func Builtin() []schemas.Metric {
	return []schemas.Metric{
		newVerifiedSuccessMetric(),
		newSafeSuccessMetric(),
		newStepsMetric(),
		newToolCallsMetric(),
		newTokensMetric(),
		newCostUSDMetric(),
		newWallTimeMetric(),
		newFalseCompletionMetric(),
		newClaimPrecisionMetric(),
		newRetryAmplificationMetric(),
		newPatternLoopMetric(),
		newPatternUnverifiedMetric(),
		newPatternIgnoredErrorMetric(),
		newSecretExposedMetric(),
		newSecretContextMetric(),
		newMemorySeenMetric(),
		newMemoryDisclosedMetric(),
	}
}
