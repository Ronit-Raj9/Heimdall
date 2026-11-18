import type {
	LiveComparisonResponse,
	LivePullResponse,
	LiveSessionListResponse,
	LiveStatusResponse,
} from "@/components/livescore/livescore.types";
import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

/** Filter of GET /livescore/sessions. */
export interface LiveSessionsArgs {
	version?: string;
	flag?: string;
	limit?: number;
}

/**
 * X6 live scoring (docs/live-scoring.md), injected into the bench API so the shared endpoint list
 * stays untouched. Reads take no token; a pull is a write (it changes local files, and it only reads
 * the gateway), so it carries the write token like every bench mutation. A pull refreshes the
 * status, sessions and comparison.
 */
export const livescoreApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchLive"] }).injectEndpoints({
	endpoints: (builder) => ({
		getLivescore: builder.query<LiveStatusResponse, void>({
			query: () => "/livescore",
			providesTags: [{ type: "BenchLive", id: "STATUS" }],
		}),
		getLiveSessions: builder.query<LiveSessionListResponse, LiveSessionsArgs>({
			query: (args) => liveSessionsPath(args),
			providesTags: [{ type: "BenchLive", id: "SESSIONS" }],
		}),
		getLiveCompare: builder.query<LiveComparisonResponse, void>({
			query: () => "/livescore/compare",
			providesTags: [{ type: "BenchLive", id: "COMPARE" }],
		}),
		pullLivescore: builder.mutation<LivePullResponse, void>({
			query: () => livePullRequest(),
			invalidatesTags: [
				{ type: "BenchLive", id: "STATUS" },
				{ type: "BenchLive", id: "SESSIONS" },
				{ type: "BenchLive", id: "COMPARE" },
			],
		}),
	}),
});

/** GET /livescore/sessions with only the filters that are set. */
export function liveSessionsPath(args: LiveSessionsArgs): string {
	const q = new URLSearchParams();
	if (args.version) q.set("version", args.version);
	if (args.flag) q.set("flag", args.flag);
	if (args.limit) q.set("limit", String(args.limit));
	const s = q.toString();
	return s ? `/livescore/sessions?${s}` : "/livescore/sessions";
}

/** POST /livescore/pull. */
export function livePullRequest(): FetchArgs {
	return { url: "/livescore/pull", method: "POST" };
}

export const { useGetLivescoreQuery, useGetLiveSessionsQuery, useGetLiveCompareQuery, usePullLivescoreMutation } = livescoreApi;
