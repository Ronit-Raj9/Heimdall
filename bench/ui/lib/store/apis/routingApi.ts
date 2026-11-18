import type { RoutingSummaryResponse, RunRoutingResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * W9 routing policy under test (docs/routing-experiments.md): GET /experiments/{id}/routing and
 * GET /runs/{id}/routing, injected into the bench API so the shared endpoint list stays untouched.
 */
export const routingApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getExperimentRouting: builder.query<RoutingSummaryResponse, string>({
			query: (expId) => experimentRoutingPath(expId),
		}),
		getRunRouting: builder.query<RunRoutingResponse, string>({
			query: (runId) => runRoutingPath(runId),
		}),
	}),
});

/** The request path of an experiment's gateway policy summary, the id escaped as one path segment. */
export function experimentRoutingPath(expId: string): string {
	return `/experiments/${encodeURIComponent(expId)}/routing`;
}

/** The request path of one run's gateway policy view, the id escaped as one path segment. */
export function runRoutingPath(runId: string): string {
	return `/runs/${encodeURIComponent(runId)}/routing`;
}

export const { useGetExperimentRoutingQuery, useGetRunRoutingQuery } = routingApi;