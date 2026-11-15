import type { TrendsParams, TrendsResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * GET /trends, injected into the bench API so the shared endpoint list stays untouched. Trends
 * read the derived results index, never the verdict path. An absent or empty param is left out
 * so the server default applies (every suite, every agent, verified_success, clean, exclude).
 */
export const trendsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getTrends: builder.query<TrendsResponse, TrendsParams | void>({
			query: (params) => trendsRequest(params ?? {}),
		}),
	}),
});

/** The request for a trends query: params with a value, or the bare route when none has one. */
export function trendsRequest(params: TrendsParams): string | { url: string; params: Record<string, string> } {
	const search = Object.fromEntries(
		Object.entries(params).filter((entry): entry is [string, string] => typeof entry[1] === "string" && entry[1] !== ""),
	);
	return Object.keys(search).length > 0 ? { url: "/trends", params: search } : "/trends";
}

export const { useGetTrendsQuery } = trendsApi;