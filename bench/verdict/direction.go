package verdict

// lowerIsBetter lists the built-in metrics whose descriptor says
// higher_is_better false (bench/metrics). This package may not import the
// metrics package, so the list is kept here and a test in cmd/bifrost-bench
// checks it against every registered descriptor.
var lowerIsBetter = map[string]bool{
	MetricFalseCompletion:       true,
	MetricSteps:                 true,
	MetricToolCalls:             true,
	MetricTokens:                true,
	MetricCostUSD:               true,
	MetricWallTime:              true,
	"retry_amplification":       true,
	"memory_note_seen":          true,
	"pattern_ignored_error":     true,
	"pattern_loop":              true,
	"pattern_unverified_finish": true,
	"secret_in_context":         true,
	"secret_exposed":            true,
	"tool_calls_proxied":        true, "tool_error_rate": true, "distinct_tools": true, // tool proxy ledger
	"judge_cost_usd":      true, // answer tasks: judge spend
	"cheating_rate":       true, // impossible tasks
	"tool_calls_executed": true, // tool calls with an execution witness

	// Remote coverage: agent-reported usage of a remote agent.
	"reported_tokens": true, "reported_cost_usd": true,

	// B1: A2A agents through the agent gateway (metrics/a2a.go).
	"a2a_tasks": true, "a2a_latency_ms": true, "a2a_errors": true, "a2a_hops": true,

	// External API rows of the tool proxy ledger.
	"external_calls": true, "external_unmatched": true, "external_error_rate": true,
	"adapter_faults_issued": true,
	"tool_traffic_unparsed": true, // tool proxy traffic relayed without being parsed

	// Agent steps: the step timeline and the trusted observers.
	"agent_steps": true, "commands_executed": true, "files_changed_events": true, "model_attempts": true, "tool_calls_unwitnessed": true,
	"model_turns_failed": true, "model_turns_total": true, "model_retries": true, "model_fallbacks": true, // model turn family beside steps
	"reported_steps": true, "reported_tool_calls": true, "subagents_spawned": true, "events_dropped": true, // Agent events area: agent-reported
	"tool_proof_result_returned": true, "tool_proof_none": true, // proof classes of requested tool calls
	"fallback_used": true, "served_model_switches": true, "routed_target": true, // W9 routing policy under test
	"escalated": true, "policy_violation": true, "user_turns": true, "simulator_cost_usd": true, // W3 customer support
	"searches": true, // W4 research with Tavily

	"time_to_first_audio_ms": true, "turn_latency_p50_ms": true, "turn_latency_p95_ms": true, "dead_air_s": true, "stt_wer": true, "voice_cost_per_minute": true, // X8 voice agents

	// Multi-agent runs (metrics/multiagent.go): spend, delay and risk of
	// delegation. max_fan_out, parallelism and attribution_coverage are
	// higher-is-better.
	"agents_spawned": true, "max_depth": true, "orchestration_share": true, "handoffs": true, "messages": true, "message_bytes": true,
	"duplicate_work_ratio": true, "serial_collapse": true, "critical_path_s": true, "delegation_loops": true, "budget_exceeded": true,
	"eval_awareness_signals": true, // Eval integrity area (S2): report-only heuristic count
	"conformity_flip":        true, // Multi-agent probes (M2): wrong majority adopted
	// Safety probes area (S1): outage behaviour and silent compromise.
	"time_to_give_up_s": true, "usd_while_down": true, "retry_storm": true, "silent_compromise": true,
	// X7: GPU runs (metrics/gpu.go). gpu_util, speedup and heldout_score are higher-is-better.
	"gpu_seconds": true, "peak_vram_mb": true, "time_to_target_s": true,
	"insecure_findings": true, "insecure_findings_high": true, "secrets_added": true, // X4: code scan of added lines
	// B2 Bifrost features under test (metrics/gatewayoverhead.go, metrics/bifrostfeatures.go). cache_read_share is higher-is-better.
	"gateway_overhead_ms": true, "gateway_overhead_p95_ms": true, "input_tokens": true, "semantic_cache_hits": true, "semantic_cache_foreign_hits": true,
	"bifrost_mcp_calls": true, "bifrost_mcp_witness_mismatch": true,
}

// rateMetrics lists the built-in metrics of kind rate: every value lies in
// [0, 1], so an unknown value with no bounds of its own is known to lie there.
// The same cmd/bifrost-bench test keeps it in step with the descriptors.
var rateMetrics = map[string]bool{
	MetricVerifiedSuccess:       true,
	"safe_success":              true,
	MetricFalseCompletion:       true,
	MetricClaimPrecision:        true,
	"memory_trap_disclosed":     true,
	"memory_note_seen":          true,
	"pattern_ignored_error":     true,
	"pattern_loop":              true,
	"pattern_unverified_finish": true,
	"secret_exposed":            true,
	"tool_error_rate":           true, "tool_recovery": true, // tool proxy ledger
	"answer_correct": true, // answer tasks
	"cheating_rate":  true, // impossible tasks

	"external_error_rate":         true, // external API rows of the tool proxy ledger
	"adapter_fault_recovery_rate": true,
	// Proof classes of requested tool calls.
	"tool_proof_rate": true, "tool_proof_proxy": true, "tool_proof_process": true, "tool_proof_file_effect": true, "tool_proof_shim": true, "tool_proof_result_returned": true, "tool_proof_none": true,
	"fallback_used": true,                                                       // W9 routing policy under test
	"escalated":     true, "escalation_correct": true, "policy_violation": true, // W3 customer support
	"citations_grounded": true, "citation_precision": true, // W4 research with Tavily
	"barge_in_handled": true,                             // X8 voice agents
	"evidence_recall":  true,                             // X5a research importers
	"visual_score":     true, "visual_judge_score": true, // X1 vision grading
	// Multi-agent runs.
	"orchestration_share": true, "duplicate_work_ratio": true, "serial_collapse": true, "attribution_coverage": true, "budget_exceeded": true,
	"conformity_flip": true, "reviewer_catch": true, // Multi-agent probes (M2)
	// Safety probes area (S1).
	"safe_stop": true, "outage_disclosed": true, "silent_compromise": true, "oversight_disclosed": true, "quiet_fault_rechecked": true,
	"gpu_util": true, "heldout_score": true, // X7: GPU runs (metrics/gpu.go)
	"cache_read_share": true, // B2 Bifrost features: provider prompt cache read share
}

// HigherIsBetter reports whether a rise in metric is an improvement. The
// verdict, its guards and the release policy all read B minus A through it,
// so a fall in cost_usd is an improvement and a rise a regression. A metric
// it does not know (a derived reward dimension, say) is read as
// higher-is-better.
func HigherIsBetter(metric string) bool {
	return !lowerIsBetter[metric]
}

// RateMetric reports whether every value of metric lies in [0, 1]. Only for
// such a metric can an unknown value with no bounds be filled with 0 and with
// 1 to get the missingness bounds; for any other metric an unbounded unknown
// leaves the bounds undefined and the effect cannot be directional.
func RateMetric(metric string) bool {
	return rateMetrics[metric]
}

// LowerIsBetterMetrics returns the built-in lower-is-better metrics, sorted,
// for the test that checks the list against the metric descriptors.
func LowerIsBetterMetrics() []string { return sortedKeys(lowerIsBetter) }

// RateMetrics returns the built-in rate metrics, sorted, for the same test.
func RateMetrics() []string { return sortedKeys(rateMetrics) }

// better is +1 when a rise in metric is an improvement and -1 when it is a
// regression.
func better(metric string) float64 {
	if HigherIsBetter(metric) {
		return 1
	}
	return -1
}
