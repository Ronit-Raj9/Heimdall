import type { CatalogAddRequest, CatalogAddResponse, CatalogDatasetResponse, CatalogListResponse } from "@/lib/types/catalog";
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
		uploadCatalogDataset: builder.mutation<CatalogDatasetResponse, { id: string; file: File }>({
			// Raw body (named by ?filename=) so the shared base query keeps auth, CSRF and org headers.
			query: ({ id, file }) => ({
				url: catalogDatasetPath(id, file.name),
				method: "POST",
				body: file,
				headers: { "Content-Type": file.type || "application/octet-stream" },
			}),
		}),
	}),
});

/** The add path of an entry, the id escaped as one path segment. */
export function catalogAddPath(id: string): string {
	return `/benchmarks/catalog/${encodeURIComponent(id)}/add`;
}

/** The dataset upload path of an entry. */
export function catalogDatasetPath(id: string, fileName: string): string {
	return `/benchmarks/catalog/${encodeURIComponent(id)}/dataset?filename=${encodeURIComponent(fileName)}`;
}

export const { useGetBenchCatalogQuery, useAddCatalogEntryMutation, useUploadCatalogDatasetMutation } = catalogApi;
