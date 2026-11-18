import type {
	CostExplainParams,
	CostExplainResponse,
	ExperimentParetoParams,
	LeaderboardParetoParams,
	MigrationParams,
	MigrationResponse,
	ParetoResponse,
} from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * The migration report, the cost explanation and the Pareto planes (docs/model-migration.md,
 * docs/cost-explain.md), injected into the bench API so the shared endpoint list stays untouched.
 * Every route is a read over sealed runs; none takes the write token.
 */
export const migrationApi = benchApi
	.enhanceEndpoints({ addTagTypes: ["BenchMigration", "BenchCostExplain", "BenchPareto"] })
	.injectEndpoints({
		endpoints: (builder) => ({
			getMigration: builder.query<MigrationResponse, MigrationParams>({
				query: ({ expId, ...params }) => withParams(`${experimentPath(expId)}/migration`, params),
				providesTags: (_result, _error, { expId }) => [{ type: "BenchMigration", id: expId }],
			}),
			getCostExplain: builder.query<CostExplainResponse, CostExplainParams>({
				query: ({ expId, ...params }) => withParams(`${experimentPath(expId)}/cost-explain`, params),
				providesTags: (_result, _error, { expId }) => [{ type: "BenchCostExplain", id: expId }],
			}),
			getExperimentPareto: builder.query<ParetoResponse, ExperimentParetoParams>({
				query: ({ expId, ...params }) => withParams(`${experimentPath(expId)}/pareto`, params),
				providesTags: (_result, _error, { expId }) => [{ type: "BenchPareto", id: expId }],
			}),
			getLeaderboardPareto: builder.query<ParetoResponse, LeaderboardParetoParams | void>({
				query: (params) => withParams("/leaderboard/pareto", params ?? {}),
				providesTags: [{ type: "BenchPareto", id: "BOARD" }],
			}),
		}),
	});

/** One experiment's path, with the id escaped as one path segment. */
export function experimentPath(expId: string): string {
	return `/experiments/${encodeURIComponent(expId)}`;
}

/** The request: the params with a value, or the bare route when none has one. */
export function withParams(url: string, params: object): string | { url: string; params: Record<string, string> } {
	const search: Record<string, string> = {};
	for (const [key, value] of Object.entries(params)) {
		if (value !== undefined && value !== null && String(value) !== "") search[key] = String(value);
	}
	return Object.keys(search).length > 0 ? { url, params: search } : url;
}

export const { useGetMigrationQuery, useGetCostExplainQuery, useGetExperimentParetoQuery, useGetLeaderboardParetoQuery } = migrationApi;