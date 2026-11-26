import type { SuiteSummary, SuitesResponse, WithMode } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * GET /suites?fields=light (fix wave 2, E1): every benchmark with its id, version, track, task counts, readiness
 * and source, without per-task lists. The server never waits for a pack load on this list, so pickers fill at once.
 * It shares the LIST tag with GET /suites, so every change that refreshes the full list refreshes this one too.
 */
export const suitesLightApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getSuitesLight: builder.query<SuitesResponse, void>({
			query: () => "/suites?fields=light",
			providesTags: [{ type: "BenchSuites", id: "LIST" }],
		}),
		// N194: GET /suites/{id}, one benchmark's full summary, so the benchmark page never loads the whole list.
		// It shares the LIST tag, so every change that refreshes the list refreshes it too; 404 is a benchmark not listed.
		getSuite: builder.query<WithMode<SuiteSummary>, string>({
			query: (id) => `/suites/${encodeURIComponent(id)}`,
			providesTags: [{ type: "BenchSuites", id: "LIST" }],
		}),
	}),
});

export const { useGetSuitesLightQuery, useGetSuiteQuery } = suitesLightApi;