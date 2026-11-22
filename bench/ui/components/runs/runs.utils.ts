import type { BenchEvent, GridRow, MetricDescriptor, Outcome, RunTiming, Tokens, Value, ValueKind } from "@/lib/types/bench";
import { formatDuration } from "./traceView.utils";

/**
 * The per-run metrics shown side by side (DESIGN.md 9.5, 18 screen 2), with the kind used when the catalog has no descriptor.
 * `family` rows sit under the row before them (steps: turns answered, then failed turns, attempts and agent steps, so steps is
 * never read as everything the agent did; tool calls: how each requested call was proven). `optional` rows show only when some
 * run recorded the metric: runs sealed before it existed never carry it, and a row of blanks would read as unknown.
 */
export const STRIP_METRICS = [
	{ name: "steps", kind: "count" },
	{ name: "model_turns_failed", kind: "count", family: true, optional: true },
	{ name: "model_attempts", kind: "count", family: true, optional: true },
	{ name: "agent_steps", kind: "count", family: true, optional: true },
	{ name: "tool_calls", kind: "count" },
	{ name: "tool_proof_rate", kind: "rate", family: true, optional: true },
	{ name: "tool_proof_proxy", kind: "rate", family: true, optional: true },
	{ name: "tool_proof_process", kind: "rate", family: true, optional: true },
	{ name: "tool_proof_file_effect", kind: "rate", family: true, optional: true },
	{ name: "tool_proof_shim", kind: "rate", family: true, optional: true },
	{ name: "tool_proof_result_returned", kind: "rate", family: true, optional: true },
	{ name: "tool_proof_none", kind: "rate", family: true, optional: true },
	{ name: "tool_traffic_unparsed", kind: "count", family: true, optional: true },
	// Agent events area: what the agent reported about itself (agent-reported, never trusted).
	{ name: "reported_steps", kind: "count", family: true, optional: true },
	{ name: "reported_tool_calls", kind: "count", family: true, optional: true },
	{ name: "subagents_spawned", kind: "count", family: true, optional: true },
	{ name: "events_dropped", kind: "count", family: true, optional: true },
	{ name: "tokens", kind: "tokens" },
	{ name: "cost_usd", kind: "usd" },
	{ name: "wall_time_s", kind: "duration" },
	{ name: "verified_success", kind: "rate" },
	// Safety probes area (S1): a veto gate fired and the final message did not disclose it.
	{ name: "silent_compromise", kind: "rate", optional: true },
	// B2 Bifrost features: the gateway's own overhead, and Bifrost's MCP log against the tool ledger.
	{ name: "gateway_overhead_ms", kind: "count", optional: true },
	{ name: "gateway_overhead_p95_ms", kind: "count", optional: true },
	{ name: "bifrost_mcp_witness_mismatch", kind: "count", optional: true },
] as const satisfies readonly { name: string; kind: ValueKind; family?: boolean; optional?: boolean }[];

export type StripMetricName = (typeof STRIP_METRICS)[number]["name"];

/** The strip rows to show: every fixed row, and an optional row only when some lane recorded it. */
export function visibleStripMetrics(lanes: readonly { metrics: Record<string, Value | undefined> }[]): (typeof STRIP_METRICS)[number][] {
	return STRIP_METRICS.filter((m) => !("optional" in m && m.optional) || lanes.some((lane) => lane.metrics[m.name] !== undefined));
}

/** The catalog's kind for each strip metric, so a changed descriptor changes the renderer. */
export function stripMetricKinds(catalogMetrics: readonly MetricDescriptor[] | undefined): Record<StripMetricName, ValueKind> {
	const out = {} as Record<StripMetricName, ValueKind>;
	for (const { name, kind } of STRIP_METRICS) {
		out[name] = catalogMetrics?.find((m) => m.name === name)?.kind ?? kind;
	}
	return out;
}

/** Events by event id, for looking up the ids an AlignedStep carries. */
export function indexEvents(events: readonly BenchEvent[] | undefined): Map<string, BenchEvent> {
	const out = new Map<string, BenchEvent>();
	for (const event of events ?? []) out.set(event.event_id, event);
	return out;
}

/** All four disjoint token buckets. */
export function tokenTotal(tokens: Tokens): number {
	return tokens.fresh + tokens.cache_read + tokens.cache_write + tokens.out;
}

/** Outcomes that score 0 under the verifier (DESIGN.md 16.3); pass is 1; the rest are unknown or not counted. */
export function outcomeTone(outcome: Outcome | null | undefined): "pass" | "zero" | "unknown" {
	if (outcome === "pass") return "pass";
	if (
		outcome === "fail" ||
		outcome === "timeout" ||
		outcome === "budget" ||
		outcome === "crash" ||
		outcome === "refusal" ||
		outcome === "integrity_violation"
	) {
		return "zero";
	}
	return "unknown";
}

/**
 * Sums Values without ever turning an unknown into zero. All observed: the sum. Any unknown:
 * unknown, with bounds when every unknown has them. Nothing to sum: unknown with emptyReason.
 */
export function sumValues(values: readonly (Value | undefined)[], emptyReason: string): Value {
	if (values.length === 0) return { state: "unknown", v: null, reason: emptyReason };
	let observed = 0;
	let lo = 0;
	let hi = 0;
	let boundsKnown = true;
	let reason: string | undefined;
	let anyUnknown = false;
	let allNotApplicable = true;
	for (const value of values) {
		if (value?.state === "not_applicable") continue;
		allNotApplicable = false;
		if (value?.state === "observed" && typeof value.v === "number" && Number.isFinite(value.v)) {
			observed += value.v;
			continue;
		}
		anyUnknown = true;
		reason ??= value?.reason;
		if (value?.bounds) {
			lo += value.bounds[0];
			hi += value.bounds[1];
		} else {
			boundsKnown = false;
		}
	}
	if (allNotApplicable) return { state: "not_applicable", v: null };
	if (!anyUnknown) return { state: "observed", v: observed, evidence: "gateway" };
	return {
		state: "unknown",
		v: null,
		evidence: "gateway",
		reason: reason ?? "partially_observed",
		...(boundsKnown ? { bounds: [observed + lo, observed + hi] as [number, number] } : {}),
	};
}

/** Gateway event types that describe a model call, as opposed to tool calls read from content logs. */
const MODEL_CALL_TYPES = new Set<BenchEvent["type"]>(["model_request", "model_response", "retry", "fallback", "fault", "error"]);

export interface GatewaySummary {
	/** Gateway events about model calls, in sequence order. */
	calls: BenchEvent[];
	requests: number;
	retries: number;
	fallbacks: number;
	tokens: Value;
	cost: Value;
}

/** Reason code on gateway totals when the gateway could not return every row of the run. */
export const INCOMPLETE_REASON = "gateway_record_incomplete";

/**
 * The gateway lane of one run: Bifrost's rows only (source "gateway"). Retries and fallbacks
 * come from their own events when the gateway emits them, else from attempt numbers and a
 * served model that differs from the requested one. Totals are unknown when the record is incomplete.
 */
export function gatewaySummary(events: readonly BenchEvent[], complete: boolean): GatewaySummary {
	const calls = events.filter((e) => e.source === "gateway" && MODEL_CALL_TYPES.has(e.type)).sort((a, b) => a.seq - b.seq);
	const requests = calls.filter((e) => e.type === "model_request");
	const responses = calls.filter((e) => e.type === "model_response");
	const retryEvents = calls.filter((e) => e.type === "retry").length;
	const fallbackEvents = calls.filter((e) => e.type === "fallback").length;
	const retries = retryEvents > 0 ? retryEvents : requests.filter((e) => (e.attempt_no ?? 1) > 1).length;
	const fallbacks = fallbackEvents > 0 ? fallbackEvents : responses.filter(isServedByOther).length;

	const withTokens = calls.filter((e) => e.tokens);
	const tokens: Value = complete
		? withTokens.length > 0
			? { state: "observed", v: withTokens.reduce((sum, e) => sum + tokenTotal(e.tokens!), 0), evidence: "gateway" }
			: { state: "unknown", v: null, evidence: "gateway", reason: "no_gateway_rows" }
		: { state: "unknown", v: null, evidence: "gateway", reason: INCOMPLETE_REASON };
	const priced = calls.filter((e) => e.usd);
	const cost: Value = complete
		? sumValues(
				priced.map((e) => e.usd),
				"no_gateway_rows",
			)
		: { state: "unknown", v: null, evidence: "gateway", reason: INCOMPLETE_REASON };

	return { calls, requests: requests.length, retries, fallbacks, tokens, cost };
}

/** The gateway served a different model than the one requested. */
export function isServedByOther(event: BenchEvent): boolean {
	return Boolean(event.model_requested && event.model_served && event.model_requested !== event.model_served);
}

export interface PairCell {
	pairId: string;
	trial: number;
	/** arm -> outcome of that arm's run, null when the slot has no run yet. */
	outcomes: Record<string, Outcome | null>;
	hasRuns: boolean;
}

/** One grid row's cells grouped into pairs, one per trial, in trial order. */
export function pairsOfRow(row: GridRow): PairCell[] {
	const byTrial = new Map<number, PairCell>();
	for (const cell of row.cells) {
		const pair = byTrial.get(cell.trial) ?? { pairId: cell.pair_id, trial: cell.trial, outcomes: {}, hasRuns: false };
		pair.outcomes[cell.arm] = cell.run_id ? cell.outcome : null;
		pair.hasRuns ||= cell.run_id !== null;
		byTrial.set(cell.trial, pair);
	}
	return [...byTrial.values()].sort((a, b) => a.trial - b.trial);
}

/** An observed number, else null. */
function observedNumber(v: Value | undefined): number | null {
	return v?.state === "observed" && typeof v.v === "number" ? v.v : null;
}

/** The run's time taken: the observed wall_time_s metric, then the older wall_ms and wall_s names, then the time from start to seal; a dash when none is known. */
export function wallText(metrics: Record<string, Value>, timing?: Pick<RunTiming, "started" | "sealed">): string {
	const s = observedNumber(metrics.wall_time_s);
	const ms = observedNumber(metrics.wall_ms);
	const old = observedNumber(metrics.wall_s);
	let total = s !== null ? s * 1000 : ms !== null ? ms : old !== null ? old * 1000 : null;
	if (total === null && timing?.started && timing.sealed) {
		const d = new Date(timing.sealed).getTime() - new Date(timing.started).getTime();
		if (Number.isFinite(d) && d >= 0) total = d;
	}
	return total === null ? "-" : formatDuration(total);
}