import { IS_FIXTURE_MODE, benchApi } from "@/lib/store/apis/benchApi";
import type { ExperimentStatus, WithMode } from "@/lib/types/bench";

// GET /experiments (Go: schemas.ExperimentList). benchApi.ts and bench.ts do not carry this
// route yet, and fixtures.ts has no route for it, so it is injected here.
// MOVE: listExperiments and ExperimentListResponse belong in lib/store/apis/benchApi.ts and
// lib/types/bench.ts, and GET /experiments -> experiments.list.json in FIXTURE_ROUTES; then this
// file and its fixture fallback go away.

/** GET /experiments */
export type ExperimentListResponse = WithMode<{ experiments: ExperimentStatus[] }>;

/** The list fixture, read directly while fixtures.ts has no route for GET /experiments. */
const LIST_FIXTURE = "experiments.list.json";

export const experimentListApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		listVerdictExperiments: builder.query<ExperimentListResponse, void>({
			async queryFn(_arg, _api, _extraOptions, baseQuery) {
				const result = await baseQuery("/experiments");
				if (!result.error || !IS_FIXTURE_MODE) {
					return result.error ? { error: result.error } : { data: result.data as ExperimentListResponse };
				}
				try {
					const response = await fetch(`${import.meta.env.BASE_URL}fixtures/${LIST_FIXTURE}`);
					if (!response.ok) return { error: result.error };
					return { data: (await response.json()) as ExperimentListResponse };
				} catch {
					return { error: result.error };
				}
			},
			providesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
	}),
	overrideExisting: false,
});

export const { useListVerdictExperimentsQuery } = experimentListApi;