import type { BifrostUse, EvaluationBifrostUse } from "@/lib/types/bifrostUse";
import { benchApi } from "./benchApi";

/** How the org and one evaluation use Bifrost (read only). */
export const bifrostUseApi = benchApi.enhanceEndpoints({ addTagTypes: ["BifrostUse"] }).injectEndpoints({
	endpoints: (builder) => ({
		getBifrostUse: builder.query<BifrostUse, void>({
			query: () => "/integrations/bifrost",
			providesTags: [{ type: "BifrostUse", id: "ORG" }],
		}),
		getEvaluationBifrostUse: builder.query<EvaluationBifrostUse, string>({
			query: (id) => `/evaluations/${encodeURIComponent(id)}/bifrost`,
			providesTags: (_r, _e, id) => [{ type: "BifrostUse", id }],
		}),
	}),
});

export const { useGetBifrostUseQuery, useGetEvaluationBifrostUseQuery } = bifrostUseApi;
