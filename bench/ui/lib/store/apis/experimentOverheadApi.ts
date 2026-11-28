import type { OverheadEffect, ReviewLog } from "@/lib/types/experimentOverhead";
import { benchApi } from "./benchApi";

const enc = encodeURIComponent;

/** Round 5 lane L2: a comparison's gateway overhead (handlers/gatewayoverhead.go) and review log (handlers/reviews.go). */
export const experimentOverheadApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getGatewayOverhead: builder.query<OverheadEffect, string>({
			query: (expId) => `/experiments/${enc(expId)}/gateway-overhead`,
		}),
		getReviewLog: builder.query<ReviewLog, string>({
			query: (expId) => `/experiments/${enc(expId)}/reviews`,
		}),
	}),
});

export const { useGetGatewayOverheadQuery, useGetReviewLogQuery } = experimentOverheadApi;