import type { CatalogAddRequest, CatalogAddResponse, CatalogListResponse } from "@/lib/types/catalog";
import { benchApi } from "./benchApi";

/** Lane L12: the benchmark catalog and one-click add. */
export const catalogApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchCatalogEntries"] }).injectEndpoints({
	endpoints: (builder) => ({
		getBenchCatalog: builder.query<CatalogListResponse, void>({
			query: () => "/benchmarks/catalog",
			providesTags: ["BenchCatalogEntries"],
		}),
		addCatalogEntry: builder.mutation<CatalogAddResponse, CatalogAddRequest>({
			query: ({ id, ...body }) => ({ url: catalogAddPath(id), method: "POST", body }),
			invalidatesTags: ["BenchCatalogEntries", "BenchCatalog", { type: "BenchSuites", id: "LIST" }, "BenchAgents", "Jobs"],
		}),
	}),
});

/** The add path of an entry, the id escaped as one path segment. */
export function catalogAddPath(id: string): string {
	return `/benchmarks/catalog/${encodeURIComponent(id)}/add`;
}

export const { useGetBenchCatalogQuery, useAddCatalogEntryMutation } = catalogApi;
