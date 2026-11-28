import { benchApi } from "@/lib/store/apis/benchApi";

/** GET /inbox/{id}/proposal/estimate: the cost of one "Draft with a model" before it starts (lane F1, PT-8). */
export interface DraftEstimate {
	model: string;
	/** False when the price list does not know the model: usd and max_usd are then 0 and are not a cost. */
	priced: boolean;
	input_tokens: number;
	output_tokens: number;
	usd: number;
	max_usd: number;
}

// Lives next to the draft panel until the lead moves it into lib/store/apis/traceDraftApi.ts.
export const draftEstimateApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getDraftEstimate: builder.query<DraftEstimate, { item: string; model: string; kind: string }>({
			query: ({ item, model, kind }) => ({
				url: `/inbox/${encodeURIComponent(item)}/proposal/estimate`,
				params: { model, kind },
			}),
		}),
	}),
	overrideExisting: false,
});

export const { useGetDraftEstimateQuery } = draftEstimateApi;