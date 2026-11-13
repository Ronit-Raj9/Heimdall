import { IS_FIXTURE_MODE, benchApi } from "@/lib/store/apis/benchApi";
import type {
	Experiment,
	ExperimentCreatedResponse,
	ExperimentResponse,
	ExperimentStatus,
	PlanResponse,
	WithMode,
} from "@/lib/types/bench";
import type { FetchBaseQueryError } from "@reduxjs/toolkit/query/react";

// TODO(lead): move to lib/types/bench.ts beside ExperimentStatus. Mirrors schemas.ExperimentList
// (GET /experiments), which bench.ts does not have yet.
export type ExperimentListResponse = WithMode<{ experiments: ExperimentStatus[] }>;

/** A write route's body plus the write token it is sent with. */
export interface WithWriteToken<T> {
	body: T;
	token: string;
}

const bearer = (token: string) => ({ Authorization: `Bearer ${token}` });

/**
 * Routes the shared benchApi lacks: the experiment list, and the write routes with the
 * write token the server requires on POST (lib.RequireWriteToken). Injected here, so the shared
 * API slice stays untouched; the endpoints share its cache and tags.
 */
export const experimentsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		listBenchExperiments: builder.query<ExperimentListResponse, void>({
			async queryFn(_arg, _api, _extra, baseQuery) {
				const listed = await baseQuery("/experiments");
				if (!listed.error) return { data: listed.data as ExperimentListResponse };
				if (!IS_FIXTURE_MODE) return { error: listed.error as FetchBaseQueryError };
				// Fixture mode has no list route; the one fixture experiment stands in for the list.
				const one = await baseQuery("/experiments/fixture");
				if (one.error) return { error: one.error as FetchBaseQueryError };
				const status = one.data as ExperimentResponse;
				return { data: { mode: status.mode, experiments: [status] } };
			},
			providesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
		planExperimentWithToken: builder.mutation<PlanResponse, WithWriteToken<Experiment>>({
			query: ({ body, token }) => ({ url: "/experiments/plan", method: "POST", body, headers: bearer(token) }),
		}),
		createExperimentWithToken: builder.mutation<ExperimentCreatedResponse, WithWriteToken<Experiment>>({
			query: ({ body, token }) => ({ url: "/experiments", method: "POST", body, headers: bearer(token) }),
			invalidatesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
		startExperimentWithToken: builder.mutation<ExperimentResponse, WithWriteToken<string>>({
			query: ({ body: expId, token }) => ({
				url: `/experiments/${encodeURIComponent(expId)}/start`,
				method: "POST",
				headers: bearer(token),
			}),
			invalidatesTags: (_result, _error, { body: expId }) => [
				{ type: "BenchExperiment", id: expId },
				{ type: "BenchExperiment", id: "LIST" },
			],
		}),
	}),
});

export const {
	useListBenchExperimentsQuery,
	usePlanExperimentWithTokenMutation,
	useCreateExperimentWithTokenMutation,
	useStartExperimentWithTokenMutation,
} = experimentsApi;