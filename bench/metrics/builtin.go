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
		newFallbackUsedMetric(), newServedModelSwitchesMetric(), newRoutedTargetMetric(), // W9 routing policy under test (routing.go)
		newEscalatedMetric(), newEscalationCorrectMetric(), newPolicyViolationMetric(), newUserTurnsMetric(), newSimulatorCostMetric(), // W3 customer support (support.go)
		newSearchesMetric(), newSourcesCitedMetric(), newCitationsGroundedMetric(), newCitationPrecisionMetric(), // W4 research with Tavily (research.go)
		newVoiceMetric(TimeToFirstAudioMS), newVoiceMetric(TurnLatencyP50MS), newVoiceMetric(TurnLatencyP95MS), newVoiceMetric(BargeInHandled), newVoiceMetric(DeadAirS), newVoiceMetric(STTWER), newVoiceMetric(VoiceCostPerMinute), // X8 voice agents (voice.go)
		newEvidenceRecallMetric(),                           // X5a research importers: BrowseComp-Plus evidence recall (evidencerecall.go)
		newVisualScoreMetric(), newVisualJudgeScoreMetric(), // X1 vision grading (visual.go)
		// Safety probes area (S1): outage behaviour, silent compromise, oversight disclosure, quiet fault re-check.
		newOutageMetric(outageSafeStop), newOutageMetric(outageGiveUp), newOutageMetric(outageUSD), newOutageMetric(outageStorm), newOutageMetric(outageDisclosed),
		newSilentCompromiseMetric(), newOversightDisclosedMetric(), newQuietRecheckMetric(),
		// Multi-agent runs: the agent tree, attribution and the equal-budget rule (multiagent.go).
		newAgentsSpawnedMetric(),
		newMaxDepthMetric(),
		newMaxFanOutMetric(),
		newOrchestrationShareMetric(),
		newHandoffsMetric(),
		newMessagesMetric(),
		newMessageBytesMetric(),
		newDuplicateWorkRatioMetric(),
		newSerialCollapseMetric(),
		newCriticalPathMetric(),
		newParallelismMetric(),
		newDelegationLoopsMetric(),
		newAttributionCoverageMetric(),
		newBudgetExceededMetric(),
		newEvalAwarenessMetric(), // Eval integrity area (S2): heuristic eval awareness phrase count, report only
		// Multi-agent probes (M2): wrong-majority conformity and reviewer catch, report only.
		newConformityFlipMetric(),
		newReviewerCatchMetric(),
		newInsecureFindingsMetric(), newInsecureFindingsHighMetric(), newSecretsAddedMetric(), // X4: code scan of added lines
		// X7: GPU runs: the host-side sampler and what the verifier measured on the GPU (gpu.go).
		newGPUSecondsMetric(), newPeakVRAMMetric(), newGPUUtilMetric(), newSpeedupMetric(), newHeldoutScoreMetric(), newTimeToTargetMetric(),
		newA2AMetric(A2ATasks), newA2AMetric(A2ALatencyMS), newA2AMetric(A2AErrors), newA2AMetric(A2AHops), // B1: A2A agents through the agent gateway (a2a.go)
	}
}
