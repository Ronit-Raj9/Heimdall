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

	// External API rows of the tool proxy ledger.
	"external_calls": true, "external_unmatched": true, "external_error_rate": true,
	"adapter_faults_issued": true,
	"tool_traffic_unparsed": true, // tool proxy traffic relayed without being parsed

	// Agent steps: the step timeline and the trusted observers.
	"agent_steps": true, "commands_executed": true, "files_changed_events": true, "model_attempts": true, "tool_calls_unwitnessed": true,
	"model_turns_failed": true, "model_turns_total": true, "model_retries": true, "model_fallbacks": true, // model turn family beside steps
	"reported_steps": true, "reported_tool_calls": true, "subagents_spawned": true, "events_dropped": true, // Agent events area: agent-reported
	"tool_proof_result_returned": true, "tool_proof_none": true, // proof classes of requested tool calls
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
