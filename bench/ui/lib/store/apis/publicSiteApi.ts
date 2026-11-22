import type { DocBody } from "@/lib/types/welcome";
import type { DocSearchList } from "./welcomeApi";
import type { DownloadsManifest, PublicLibrary, PublicOverview, PublicPricing } from "@/lib/types/site";
import { benchApi } from "./benchApi";

/**
 * The public site (lane Y08): GET /public/* answers without sign-in and holds no organization
 * data, so these queries work for a visitor and for a signed-in user alike.
 */
export const publicSiteApi = benchApi.enhanceEndpoints({ addTagTypes: ["PublicSite"] }).injectEndpoints({
	endpoints: (builder) => ({
		getPublicOverview: builder.query<PublicOverview, void>({ query: () => "/public/overview", providesTags: ["PublicSite"] }),
		getPublicLibrary: builder.query<PublicLibrary, void>({ query: () => "/public/benchmarks", providesTags: ["PublicSite"] }),
		getPublicPricing: builder.query<PublicPricing, void>({ query: () => "/public/pricing", providesTags: ["PublicSite"] }),
		getPublicDownloads: builder.query<DownloadsManifest, void>({ query: () => "/public/downloads", providesTags: ["PublicSite"] }),
		listPublicDocs: builder.query<DocSearchList, string | void>({
			query: (q) => (q ? `/public/docs?q=${encodeURIComponent(q)}` : "/public/docs"),
			providesTags: ["PublicSite"],
		}),
		getPublicDoc: builder.query<DocBody, string>({
			query: (name) => `/public/docs/${encodeURIComponent(name)}`,
			providesTags: (_r, _e, name) => [{ type: "PublicSite", id: name }],
		}),
	}),
});

export const {
	useGetPublicOverviewQuery,
	useGetPublicLibraryQuery,
	useGetPublicPricingQuery,
	useGetPublicDownloadsQuery,
	useListPublicDocsQuery,
	useGetPublicDocQuery,
} = publicSiteApi;