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
		}),
	}),
});

/** The request path of an experiment's trials and tier panel, the id escaped as one path segment. */
export function experimentAdaptivePath(expId: string): string {
	return `/experiments/${encodeURIComponent(expId)}/adaptive`;
}

export const { useGetExperimentAdaptiveQuery } = adaptiveApi;