import type { ExtendPoolView } from "@/lib/types/extendPool";
import { benchApi } from "./benchApi";

/** Round 6 N6: a comparison read together with its Extend follow-ups, GET /experiments/{id}/extensions. */
export const extendPoolApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getExperimentExtensions: builder.query<ExtendPoolView, string>({
			query: (expId) => `/experiments/${encodeURIComponent(expId)}/extensions`,
			keepUnusedDataFor: 600,
			providesTags: (_result, _error, expId) => [{ type: "BenchExperimentResult", id: expId }],
		}),
	}),
});

export const { useGetExperimentExtensionsQuery } = extendPoolApi;