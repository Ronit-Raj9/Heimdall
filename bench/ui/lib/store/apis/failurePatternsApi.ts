import { benchApi } from "./benchApi";

/** One run that showed a failure pattern, for a link to its page. */
export interface FailureExample {
	run_id: string;
	exp_id: string;
	pair_id?: string;
	benchmark: string;
	task: string;
	outcome: string;
}

/** One pattern of a group: count runs showed it out of observed runs where it was measured. */
export interface FailurePatternCount {
	/** pattern_loop | pattern_unverified_finish | pattern_ignored_error | false_completion */
	pattern: string;
	label: string;
	description: string;
	count: number;
	observed: number;
	unknown: number;
	examples: FailureExample[];
}

/** One agent version (agent read) or one evaluation arm (evaluation read). */
export interface FailurePatternGroup {
	/** "v<N>", "config:<hash prefix>" or the evaluation arm label. */
	key: string;
	label: string;
	agent: string;
	/** 0 when the config is not a saved version. */
	version: number;
	config_hash?: string;
	models: string[];
	runs: number;
	failed: number;
	experiments: string[];
	last_at: string;
	patterns: FailurePatternCount[];
}

/** GET /agents/{id}/failure-patterns and GET /evaluations/{id}/failure-patterns. */
export interface FailurePatterns {
	agent?: string;
	evaluation?: string;
	groups: FailurePatternGroup[];
}

/** Groups with null lists filled, so screens can map over them. */
export function normalizeFailurePatterns(body: FailurePatterns | null | undefined): FailurePatterns {
	return {
		...(body ?? {}),
		groups: (body?.groups ?? []).map((g) => ({
			...g,
			models: g.models ?? [],
			experiments: g.experiments ?? [],
			patterns: (g.patterns ?? []).map((p) => ({ ...p, examples: p.examples ?? [] })),
		})),
	};
}

/** Patterns seen at least once, most frequent first (ties keep the server's order). */
export function seenPatterns(group: FailurePatternGroup): FailurePatternCount[] {
	return group.patterns
		.map((p, i) => ({ p, i }))
		.filter(({ p }) => p.count > 0)
		.sort((a, b) => b.p.count - a.p.count || a.i - b.i)
		.map(({ p }) => p);
}

/** Share of measured runs that showed the pattern, or null when none was measured. */
export function patternRate(p: Pick<FailurePatternCount, "count" | "observed">): number | null {
	return p.observed > 0 ? p.count / p.observed : null;
}

const enc = encodeURIComponent;

/** The failure pattern reads, injected into the bench API so the shared endpoint list stays untouched. */
export const failurePatternsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getAgentFailurePatterns: builder.query<FailurePatterns, string>({
			query: (agent) => `/agents/${enc(agent)}/failure-patterns`,
			transformResponse: (body: FailurePatterns) => normalizeFailurePatterns(body),
		}),
		getEvaluationFailurePatterns: builder.query<FailurePatterns, string>({
			query: (evaluation) => `/evaluations/${enc(evaluation)}/failure-patterns`,
			transformResponse: (body: FailurePatterns) => normalizeFailurePatterns(body),
		}),
	}),
});

export const { useGetAgentFailurePatternsQuery, useGetEvaluationFailurePatternsQuery } = failurePatternsApi;
