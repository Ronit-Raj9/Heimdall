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
		newToolCallsExecutedMetric(),
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
		newRewardMetric(),
		newAnswerCorrectMetric(), newJudgeCostMetric(), // answer tasks: per-category success, judge spend
		newToolCallsProxiedMetric(), newToolErrorRateMetric(), newToolRecoveryMetric(), newDistinctToolsMetric(), // tool proxy ledger
		newExternalCallsMetric(), newExternalUnmatchedMetric(), newExternalErrorRateMetric(), // external API rows of the tool proxy ledger
		newSkillFilesReadMetric(), // skills: installed skill files the agent read
		newCheatingRateMetric(),   // impossible tasks: passes before the impossible_pass veto
		&adapterFaultIssuedMetric{}, &adapterFaultRecoveryMetric{},
		// Tool proxy traffic relayed without being parsed.
		newToolTrafficUnparsedMetric(),
		// Agent steps: the step timeline and the trusted observers.
		newAgentStepsMetric(), newCommandsExecutedMetric(), newFilesChangedMetric(), newModelAttemptsMetric(), newToolCallsUnwitnessedMetric(),
		newModelTurnsFailedMetric(), newModelTurnsTotalMetric(), newModelRetriesMetric(), newModelFallbacksMetric(), // model turn family beside steps
		newToolProofMetric(ToolProofRate), newToolProofMetric(ToolProofProxy), newToolProofMetric(ToolProofProcess), newToolProofMetric(ToolProofFileEffect), newToolProofMetric(ToolProofShim), newToolProofMetric(ToolProofResultReturned), newToolProofMetric(ToolProofNone), // proof class of every requested tool call
		// Remote coverage: agent-reported usage of a remote agent, apart from tokens and cost_usd.
		newReportedTokensMetric(), newReportedCostMetric(),
		newReportedStepsMetric(), newReportedToolCallsMetric(), newSubagentsSpawnedMetric(), newEventsDroppedMetric(), // Agent events area: agent-reported (reported.go)
	}
}
