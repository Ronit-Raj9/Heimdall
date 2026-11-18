import type { CanaryDetailResponse, CanaryListResponse, CanaryRunAcceptedResponse } from "@/lib/types/bench";
import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

/**
 * W5 drift canary (docs/drift-canary.md), injected into the bench API so the shared endpoint list
 * stays untouched. Reads take no token; a run is a write, so it carries the write token like every
 * bench mutation (prepareBenchHeaders). The run starts in the background and answers 202; the
 * list and the canary are refreshed so the page can poll for the new look.
 */
export const canaryApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchCanary"] }).injectEndpoints({
	endpoints: (builder) => ({
		getCanaries: builder.query<CanaryListResponse, void>({
			query: () => "/canaries",
			providesTags: [{ type: "BenchCanary", id: "LIST" }],
		}),
		getCanary: builder.query<CanaryDetailResponse, string>({
			query: (name) => canaryPath(name),
			providesTags: (_result, _error, name) => [{ type: "BenchCanary", id: name }],
		}),
		runCanary: builder.mutation<CanaryRunAcceptedResponse, string>({
			query: (name) => canaryRunRequest(name),
			invalidatesTags: (_result, _error, name) => [
				{ type: "BenchCanary", id: "LIST" },
				{ type: "BenchCanary", id: name },
			],
		}),
	}),
});

/** One canary's path, with the name escaped as one path segment. */
export function canaryPath(name: string): string {
	return `/canaries/${encodeURIComponent(name)}`;
}

/** POST /canaries/{name}/run. */
export function canaryRunRequest(name: string): FetchArgs {
	return { url: `${canaryPath(name)}/run`, method: "POST" };
}

export const { useGetCanariesQuery, useGetCanaryQuery, useRunCanaryMutation } = canaryApi;