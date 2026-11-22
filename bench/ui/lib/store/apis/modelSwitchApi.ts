import type { Pareto, Value } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * Model switch report and Bifrost routing export (lane Z21, bench/modelswitch). The types mirror
 * the Go JSON exactly.
 */

export type ComparisonResult = "best" | "worse" | "cannot_tell" | "no_data" | "better";
export type SwitchAction = "keep" | "switch" | "not_enough_evidence" | "still_running";

export interface SwitchModel {
	label: string;
	model: string;
	provider: string;
	open?: boolean;
	runs: number;
	tasks: number;
	quality: Value;
	cost_per_run: Value;
	cost_per_success: Value;
	cost_total: Value;
	latency_p50_ms: Value;
	latency_p95_ms: Value;
	wall_p50_s: Value;
	availability: Value;
	model_calls: number;
	call_failures: number;
	fallbacks: number;
	excluded?: Record<string, number>;
	frontier: boolean;
	latency_frontier: boolean;
}

export interface SwitchComparison {
	label: string;
	against: string;
	tasks: number;
	mean_diff: number;
	diff_low: number | null;
	diff_high: number | null;
	p: number | null;
	p_adjusted: number | null;
	result: ComparisonResult;
	reason: string;
}

export interface SwitchGroupModel {
	runs: number;
	tasks: number;
	quality: Value;
	cost_per_run: Value;
	latency_p50_ms: Value;
}

export interface SwitchGroup {
	key: string;
	value: string;
	tasks: number;
	models: Record<string, SwitchGroupModel>;
	best: string;
	default: string;
	vs_default?: SwitchComparison;
	small?: boolean;
	note?: string;
}

export interface LatencyPoint {
	label: string;
	model: string;
	latency_ms: Value;
	quality: Value;
	placed: boolean;
	frontier: boolean;
	reason?: string;
}

export interface SwitchRecommendation {
	action: SwitchAction;
	model: string;
	strength: "strong" | "weak";
	summary: string;
	reasons: string[];
	caveats: string[];
	tied: string[];
}

/** GET /evaluations/{id}/model-switch */
export interface ModelSwitchReport {
	evaluation: string;
	status: string;
	agent: string;
	version?: number;
	current: string;
	alpha: number;
	best: string;
	models: SwitchModel[];
	comparisons: SwitchComparison[];
	pareto: Pareto;
	latency_pareto: { points: LatencyPoint[]; frontier: string[]; note: string };
	benchmarks: SwitchGroup[];
	segments: SwitchGroup[];
	recommendation: SwitchRecommendation;
	missing: string[];
	notes: string[];
	practice_runs?: number;
}

export interface BifrostRoutingRule {
	name: string;
	description?: string;
	enabled: boolean;
	cel_expression: string;
	targets: { provider?: string; model?: string; weight: number }[];
	fallbacks?: string[];
	scope: string;
	scope_id?: string;
	priority: number;
}

/** The downloadable Bifrost config.json fragment. */
export interface BifrostConfig {
	$schema: string;
	governance: { routing_rules: BifrostRoutingRule[] };
}

export interface RoutingOptions {
	prefix?: string;
	header_prefix?: string;
	scope?: string;
	scope_id?: string;
	base_priority?: number;
	budget_saver_at?: number;
}

/** GET /evaluations/{id}/model-switch/bifrost-config */
export interface ModelSwitchExport {
	evaluation: string;
	ready: boolean;
	config: BifrostConfig;
	rules: { name: string; kind: "budget_saver" | "segment" | "default"; enabled: boolean; why: string; evidence: string }[];
	headers: { header: string; key: string; values: string[] }[];
	budget_hints: { label: string; model: string; cost_per_1000_runs: Value; text: string }[];
	options: RoutingOptions;
	notes: string[];
}

export interface ModelSwitchApplyRequest extends RoutingOptions {
	gateway_url: string;
	credential_secret?: string;
	allow_global?: boolean;
	enable?: boolean;
	dry_run?: boolean;
}

export interface ModelSwitchApplyResult {
	gateway: string;
	dry_run: boolean;
	scope: string;
	scope_id?: string;
	steps: { rule: string; action: "create" | "update"; rule_id?: string; priority: number; enabled: boolean; status: "planned" | "done" | "failed"; error?: string }[];
	stale: string[];
	notes: string[];
}

const enc = encodeURIComponent;

/** The query string of the routing options (scope, prefix ...), without empty fields. */
export function routingQuery(o: RoutingOptions): string {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(o)) {
		if (v !== undefined && v !== null && String(v).trim() !== "") q.set(k, String(v));
	}
	const s = q.toString();
	return s ? `?${s}` : "";
}

export const modelSwitchApi = benchApi.enhanceEndpoints({ addTagTypes: ["ModelSwitch"] }).injectEndpoints({
	endpoints: (builder) => ({
		getModelSwitch: builder.query<ModelSwitchReport, string>({
			query: (id) => `/evaluations/${enc(id)}/model-switch`,
			providesTags: (_r, _e, id) => [{ type: "ModelSwitch", id }],
		}),
		getModelSwitchConfig: builder.query<ModelSwitchExport, { id: string; options: RoutingOptions }>({
			query: ({ id, options }) => `/evaluations/${enc(id)}/model-switch/bifrost-config${routingQuery(options)}`,
			providesTags: (_r, _e, { id }) => [{ type: "ModelSwitch", id: `${id}/config` }],
		}),
		applyModelSwitch: builder.mutation<ModelSwitchApplyResult, { id: string; body: ModelSwitchApplyRequest }>({
			query: ({ id, body }) => ({ url: `/evaluations/${enc(id)}/model-switch/apply`, method: "POST", body }),
		}),
	}),
});

export const { useGetModelSwitchQuery, useGetModelSwitchConfigQuery, useApplyModelSwitchMutation } = modelSwitchApi;
