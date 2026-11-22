import type { Tokens } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/** One model call as Bifrost saw it (handlers/runbifrost.go BifrostCall). */
export interface BifrostCall {
	id: string;
	at: string;
	provider: string;
	model_requested: string;
	model_served: string;
	status: string;
	error_status?: number;
	fallback_index: number;
	cache_hit: boolean;
	/** The fault applied to this call; absent when none. */
	fault?: string;
	tokens?: Tokens;
	cost_usd?: number;
	latency_ms: number;
}

/** GET /runs/{id}/bifrost (lane C09): what Bifrost did for one run's model calls. */
export interface RunBifrost {
	run_id: string;
	available: boolean;
	complete: boolean;
	source: "bifrost_logs" | "plugin_usage" | "both" | "";
	providers: string[];
	models: string[];
	calls: number;
	fallbacks: number;
	cache_hits: number;
	faults: number;
	errors: number;
	tokens: Tokens;
	/** Absent when no call carried a cost. */
	cost_usd?: number;
	cost_known: number;
	latency_ms: number;
	avg_latency_ms: number;
	call_list: BifrostCall[];
	/** Where the run's faults were applied: plugin (inside Bifrost), proxy (the fault proxy behind Bifrost) or mixed; absent when none was. */
	fault_path?: "plugin" | "proxy" | "mixed";
}

export const runBifrostApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunBifrost: builder.query<RunBifrost, string>({
			query: (runId) => runBifrostPath(runId),
		}),
	}),
});

/** The request path of one run's Through Bifrost facts, the id escaped as one path segment. */
export function runBifrostPath(runId: string): string {
	return `/runs/${encodeURIComponent(runId)}/bifrost`;
}

export const { useGetRunBifrostQuery } = runBifrostApi;