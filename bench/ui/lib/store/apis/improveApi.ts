import type { ImprovementDetail, ImprovementList, ImprovementSummary } from "@/lib/types/improve";
import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

/**
 * Improvement runs (bench/improve). Reads take no token; approve and stop are writes and carry the
 * write token like every bench mutation. Both refresh the list and the run so the page can poll.
 */
export const improveApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchImprovement"] }).injectEndpoints({
	endpoints: (builder) => ({
		getImprovements: builder.query<ImprovementList, void>({
			query: () => "/improvements",
			providesTags: [{ type: "BenchImprovement", id: "LIST" }],
		}),
		getImprovement: builder.query<ImprovementDetail, string>({
			query: (id) => improvementPath(id),
			providesTags: (_r, _e, id) => [{ type: "BenchImprovement", id }],
		}),
		approveImprovementRound: builder.mutation<ImprovementSummary, { id: string; round: number; reason: string }>({
			query: (a) => approveRequest(a.id, a.round, a.reason),
			invalidatesTags: (_r, _e, a) => [
				{ type: "BenchImprovement", id: "LIST" },
				{ type: "BenchImprovement", id: a.id },
			],
		}),
		stopImprovement: builder.mutation<ImprovementSummary, string>({
			query: (id) => ({ url: `${improvementPath(id)}/stop`, method: "POST" }),
			invalidatesTags: (_r, _e, id) => [
				{ type: "BenchImprovement", id: "LIST" },
				{ type: "BenchImprovement", id },
			],
		}),
	}),
});

/** One improvement's path, the id escaped as one segment. */
export function improvementPath(id: string): string {
	return `/improvements/${encodeURIComponent(id)}`;
}

/** POST /improvements/{id}/rounds/{k}/approve with the reason. */
export function approveRequest(id: string, round: number, reason: string): FetchArgs {
	return { url: `${improvementPath(id)}/rounds/${round}/approve`, method: "POST", body: { reason } };
}

export const { useGetImprovementsQuery, useGetImprovementQuery, useApproveImprovementRoundMutation, useStopImprovementMutation } = improveApi;
