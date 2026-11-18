import type { RunAgentsResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * GET /runs/{id}/agents: the agents of a multi-agent run, their swimlanes, handoffs and blame,
 * injected into the bench API so the shared endpoint list stays untouched. A single-agent run
 * answers available false with empty lists, not an error.
 */
export const runAgentsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunAgents: builder.query<RunAgentsResponse, string>({
			query: (runId) => runAgentsPath(runId),
		}),
	}),
});

/** The request path of one run's agents, with the run id escaped as one path segment. */
export function runAgentsPath(runId: string): string {
	return `/runs/${encodeURIComponent(runId)}/agents`;
}

export const { useGetRunAgentsQuery } = runAgentsApi;