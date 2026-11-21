import type { ResearchSummaryResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * W4 research with Tavily (docs/research-tasks.md): GET /experiments/{id}/research, injected into the
 * bench API so the shared endpoint list stays untouched.
 */
export const researchApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getExperimentResearch: builder.query<ResearchSummaryResponse, string>({
			query: (expId) => experimentResearchPath(expId),
			providesTags: (_result, _error, expId) => [{ type: "BenchExperimentResult", id: expId }],
		}),
	}),
});

/** The request path of an experiment's research scorecard, the id escaped as one path segment. */
export function experimentResearchPath(expId: string): string {
	return `/experiments/${encodeURIComponent(expId)}/research`;
}

export const { useGetExperimentResearchQuery } = researchApi;
