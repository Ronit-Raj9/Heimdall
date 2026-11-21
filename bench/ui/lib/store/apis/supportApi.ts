import type { SupportSummaryResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * W3 customer support (docs/customer-support.md): GET /experiments/{id}/support, injected into the
 * bench API so the shared endpoint list stays untouched.
 */
export const supportApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getExperimentSupport: builder.query<SupportSummaryResponse, string>({
			query: (expId) => experimentSupportPath(expId),
			providesTags: (_result, _error, expId) => [{ type: "BenchExperimentResult", id: expId }],
		}),
	}),
});

/** The request path of an experiment's support scorecard, the id escaped as one path segment. */
export function experimentSupportPath(expId: string): string {
	return `/experiments/${encodeURIComponent(expId)}/support`;
}

export const { useGetExperimentSupportQuery } = supportApi;
