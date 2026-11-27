import type { AdaptiveViewResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * W7 adaptive trials and suite tiers (docs/adaptive-trials.md): GET /experiments/{id}/adaptive,
 * injected into the bench API so the shared endpoint list stays untouched.
 */
export const adaptiveApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getExperimentAdaptive: builder.query<AdaptiveViewResponse, string>({
			query: (expId) => experimentAdaptivePath(expId),
			// Slow to compute (1.7 to 2.7 s) and fixed once the comparison ends; a finish invalidates the tag (M4-6).
			keepUnusedDataFor: 600,
			providesTags: (_result, _error, expId) => [{ type: "BenchExperimentResult", id: expId }],
		}),
	}),
});

/** The request path of an experiment's trials and tier panel, the id escaped as one path segment. */
export function experimentAdaptivePath(expId: string): string {
	return `/experiments/${encodeURIComponent(expId)}/adaptive`;
}

export const { useGetExperimentAdaptiveQuery } = adaptiveApi;