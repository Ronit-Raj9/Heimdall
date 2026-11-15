import type { AgentDiffParams, AgentDiffResponse, AgentDiffVersionsResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * GET /agents/diff and GET /agents/diff/versions, injected into the bench API so the shared
 * endpoint list stays untouched. Both are read-only: the diff is computed from verified sealed
 * runs on every request and writes nothing.
 */
export const agentDiffApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getAgentDiff: builder.query<AgentDiffResponse, AgentDiffParams>({
			query: (params) => agentDiffRequest(params),
		}),
		getAgentDiffVersions: builder.query<AgentDiffVersionsResponse, void>({
			query: () => "/agents/diff/versions",
		}),
	}),
});

/** The request for a diff query: params with a value, or the bare route when none has one. */
export function agentDiffRequest(params: AgentDiffParams): string | { url: string; params: Record<string, string> } {
	const search = Object.fromEntries(
		Object.entries(params).filter((entry): entry is [string, string] => typeof entry[1] === "string" && entry[1] !== ""),
	);
	return Object.keys(search).length > 0 ? { url: "/agents/diff", params: search } : "/agents/diff";
}

export const { useGetAgentDiffQuery, useGetAgentDiffVersionsQuery } = agentDiffApi;