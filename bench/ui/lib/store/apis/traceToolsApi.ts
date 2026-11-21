import type {
	MaximConnection,
	MaximConnectionUpdate,
	MaximTestResult,
	RunMaxim,
	SearchParams,
	SearchResponse,
} from "@/lib/types/traceTools";
import { benchApi } from "./benchApi";

/** Search params without empty values, so the URL carries only what is set. */
export function searchQueryParams(p: SearchParams): Record<string, string | number> {
	const out: Record<string, string | number> = { q: p.q.trim(), limit: p.limit ?? 50 };
	for (const k of ["agent", "version", "benchmark", "outcome"] as const) {
		const v = p[k];
		if (v) out[k] = v;
	}
	return out;
}

/** Trace search across the org's runs and the Maxim log repository connection. */
export const traceToolsApi = benchApi.enhanceEndpoints({ addTagTypes: ["Maxim", "RunMaxim"] }).injectEndpoints({
	endpoints: (builder) => ({
		searchTraces: builder.query<SearchResponse, SearchParams>({
			query: (p) => ({ url: "/search", params: searchQueryParams(p) }),
		}),
		getMaxim: builder.query<MaximConnection, void>({
			query: () => "/integrations/maxim",
			providesTags: [{ type: "Maxim", id: "CONN" }],
		}),
		saveMaxim: builder.mutation<MaximConnection, MaximConnectionUpdate>({
			query: (body) => ({ url: "/integrations/maxim", method: "PUT", body }),
			invalidatesTags: [{ type: "Maxim", id: "CONN" }, "RunMaxim"],
		}),
		deleteMaxim: builder.mutation<MaximConnection, void>({
			query: () => ({ url: "/integrations/maxim", method: "DELETE" }),
			invalidatesTags: [{ type: "Maxim", id: "CONN" }, "RunMaxim"],
		}),
		testMaxim: builder.mutation<MaximTestResult, void>({
			query: () => ({ url: "/integrations/maxim/test", method: "POST" }),
		}),
		getRunMaxim: builder.query<RunMaxim, string>({
			query: (id) => `/runs/${encodeURIComponent(id)}/maxim`,
			providesTags: (_r, _e, id) => [{ type: "RunMaxim", id }],
		}),
		pushRunMaxim: builder.mutation<RunMaxim, string>({
			query: (id) => ({ url: `/runs/${encodeURIComponent(id)}/maxim/push`, method: "POST" }),
			invalidatesTags: (_r, _e, id) => [{ type: "RunMaxim", id }, { type: "Maxim", id: "CONN" }],
		}),
	}),
});

export const {
	useSearchTracesQuery,
	useGetMaximQuery,
	useSaveMaximMutation,
	useDeleteMaximMutation,
	useTestMaximMutation,
	useGetRunMaximQuery,
	usePushRunMaximMutation,
} = traceToolsApi;
