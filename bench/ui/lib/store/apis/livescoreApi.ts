import type {
	LivePullResponse,
	LiveStatusResponse,
} from "@/components/livescore/livescore.types";
import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

/**
 * X6 live scoring (docs/live-scoring.md), injected into the bench API so the shared endpoint list
 * stays untouched. Reads take no token; a pull is a write (it changes local files, and it only reads
 * the gateway), so it carries the write token like every bench mutation. A pull refreshes the
 * status.
 */
export const livescoreApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchLive"] }).injectEndpoints({
	endpoints: (builder) => ({
		getLivescore: builder.query<LiveStatusResponse, void>({
			query: () => "/livescore",
			providesTags: [{ type: "BenchLive", id: "STATUS" }],
		}),
		pullLivescore: builder.mutation<LivePullResponse, void>({
			query: () => livePullRequest(),
			invalidatesTags: [{ type: "BenchLive", id: "STATUS" }],
		}),
	}),
});

/** POST /livescore/pull. */
export function livePullRequest(): FetchArgs {
	return { url: "/livescore/pull", method: "POST" };
}

export const { useGetLivescoreQuery, usePullLivescoreMutation } = livescoreApi;
