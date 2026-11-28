import type { PairDivergenceResponse } from "./behaviourApi";
import { benchApi } from "./benchApi";

/** GET /runs/{id}/divergence/{other}: first divergence of any two runs of one task (handlers/divergence_w441.go). */
export const runDivergenceApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunDivergence: builder.query<PairDivergenceResponse, { runId: string; other: string }>({
			query: ({ runId, other }) => runDivergencePath(runId, other),
		}),
	}),
});

export function runDivergencePath(runId: string, other: string): string {
	return `/runs/${encodeURIComponent(runId)}/divergence/${encodeURIComponent(other)}`;
}

export const { useGetRunDivergenceQuery } = runDivergenceApi;
