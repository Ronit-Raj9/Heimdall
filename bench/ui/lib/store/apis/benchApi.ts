import i18n from "@/lib/i18n";
import type {
	AuditRequest,
	AuditResponse,
	CatalogResponse,
	DiagnosticsResponse,
	DoctorResponse,
	ExperimentCreatedResponse,
	ExperimentResponse,
	Experiment,
	GridResponse,
	HealthResponse,
	PairResponse,
	PlanResponse,
	RegradeRequest,
	RegradeResponse,
	RunDiffResponse,
	RunEventsResponse,
	RunReceiptsResponse,
	RunResponse,
	VerdictResponse,
} from "@/lib/types/bench";
import { createApi, fetchBaseQuery, type BaseQueryFn, type FetchArgs, type FetchBaseQueryError } from "@reduxjs/toolkit/query/react";
import { BENCH_API_BASE, detectFixtureMode, resolveFixture } from "./fixtures";

/** Polling interval for experiment status when server-sent events are unavailable (DESIGN.md 16.7). */
export const EXPERIMENT_POLL_MS = 2000;

/** Decided once at load, so the flag survives in-app navigation that drops the query string. */
export const IS_FIXTURE_MODE = detectFixtureMode(
	typeof window === "undefined" ? "" : window.location.search,
	import.meta.env.VITE_BENCH_FIXTURE,
);

/** Server-sent events URL for live experiment status. Not an RTK Query endpoint: it streams. */
export function experimentEventsUrl(expId: string): string {
	return `${BENCH_API_BASE}/experiments/${encodeURIComponent(expId)}/events`;
}

/** The message to show for a failed bench request. */
export function getBenchErrorMessage(error: unknown): string {
	if (error && typeof error === "object" && "data" in error) {
		const data = (error as { data?: { error?: { message?: unknown } } }).data;
		if (typeof data?.error?.message === "string") return data.error.message;
	}
	if (error && typeof error === "object" && "error" in error && typeof (error as { error: unknown }).error === "string") {
		return (error as { error: string }).error;
	}
	return i18n.t("errors.unexpected", { ns: "bench" });
}

type BenchBaseQuery = BaseQueryFn<string | FetchArgs, unknown, FetchBaseQueryError>;

const httpBaseQuery: BenchBaseQuery = fetchBaseQuery({
	baseUrl: BENCH_API_BASE,
	credentials: "include",
	prepareHeaders: (headers) => {
		if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
		return headers;
	},
});

const fixtureBaseQuery: BenchBaseQuery = async (args) => {
	const url = typeof args === "string" ? args : args.url;
	const method = typeof args === "string" ? "GET" : (args.method ?? "GET");
	const file = resolveFixture(method, url);
	if (!file) {
		return { error: { status: 404, data: { error: { message: i18n.t("errors.noFixture", { ns: "bench", method, url }) } } } };
	}
	try {
		const response = await fetch(`${import.meta.env.BASE_URL}fixtures/${file}`);
		if (!response.ok) {
			return { error: { status: response.status, data: { error: { message: i18n.t("errors.fixtureLoad", { ns: "bench", file }) } } } };
		}
		return { data: (await response.json()) as unknown };
	} catch (error) {
		return { error: { status: "FETCH_ERROR", error: String(error) } };
	}
};

const id = (value: string) => encodeURIComponent(value);

/**
 * Every route of DESIGN.md 16.7. Tags are per entity and per id, so a regrade refreshes one
 * experiment's analysis and nothing else.
 */
export const benchApi = createApi({
	reducerPath: "benchApi",
	baseQuery: IS_FIXTURE_MODE ? fixtureBaseQuery : httpBaseQuery,
	tagTypes: [
		"BenchHealth",
		"BenchDoctor",
		"BenchCatalog",
		"BenchExperiment",
		"BenchVerdict",
		"BenchDiagnostics",
		"BenchGrid",
		"BenchRun",
		"BenchRunEvents",
		"BenchRunDiff",
		"BenchRunReceipts",
		"BenchPair",
	],
	endpoints: (builder) => ({
		getHealth: builder.query<HealthResponse, void>({
			query: () => "/health",
			providesTags: ["BenchHealth"],
		}),
		getDoctor: builder.query<DoctorResponse, void>({
			query: () => "/doctor",
			providesTags: ["BenchDoctor"],
		}),
		getCatalog: builder.query<CatalogResponse, void>({
			query: () => "/catalog",
			providesTags: ["BenchCatalog"],
		}),

		// Experiments
		planExperiment: builder.mutation<PlanResponse, Experiment>({
			query: (experiment) => ({ url: "/experiments/plan", method: "POST", body: experiment }),
		}),
		createExperiment: builder.mutation<ExperimentCreatedResponse, Experiment>({
			query: (experiment) => ({ url: "/experiments", method: "POST", body: experiment }),
		}),
		startExperiment: builder.mutation<ExperimentResponse, string>({
			query: (expId) => ({ url: `/experiments/${id(expId)}/start`, method: "POST" }),
			invalidatesTags: (_result, _error, expId) => [{ type: "BenchExperiment", id: expId }],
		}),
		getExperiment: builder.query<ExperimentResponse, string>({
			query: (expId) => `/experiments/${id(expId)}`,
			providesTags: (_result, _error, expId) => [{ type: "BenchExperiment", id: expId }],
		}),
		getVerdict: builder.query<VerdictResponse, string>({
			query: (expId) => `/experiments/${id(expId)}/verdict`,
			providesTags: (_result, _error, expId) => [{ type: "BenchVerdict", id: expId }],
		}),
		getDiagnostics: builder.query<DiagnosticsResponse, string>({
			query: (expId) => `/experiments/${id(expId)}/diagnostics`,
			providesTags: (_result, _error, expId) => [{ type: "BenchDiagnostics", id: expId }],
		}),
		getGrid: builder.query<GridResponse, string>({
			query: (expId) => `/experiments/${id(expId)}/grid`,
			providesTags: (_result, _error, expId) => [{ type: "BenchGrid", id: expId }],
		}),

		// Runs and pairs
		getRun: builder.query<RunResponse, string>({
			query: (runId) => `/runs/${id(runId)}`,
			providesTags: (_result, _error, runId) => [{ type: "BenchRun", id: runId }],
		}),
		getRunEvents: builder.query<RunEventsResponse, string>({
			query: (runId) => `/runs/${id(runId)}/events`,
			providesTags: (_result, _error, runId) => [{ type: "BenchRunEvents", id: runId }],
		}),
		getRunDiff: builder.query<RunDiffResponse, string>({
			query: (runId) => `/runs/${id(runId)}/diff`,
			providesTags: (_result, _error, runId) => [{ type: "BenchRunDiff", id: runId }],
		}),
		getRunReceipts: builder.query<RunReceiptsResponse, string>({
			query: (runId) => `/runs/${id(runId)}/receipts`,
			providesTags: (_result, _error, runId) => [{ type: "BenchRunReceipts", id: runId }],
		}),
		getPair: builder.query<PairResponse, string>({
			query: (pairId) => `/pairs/${id(pairId)}`,
			providesTags: (_result, _error, pairId) => [{ type: "BenchPair", id: pairId }],
		}),

		// Analysis
		auditTable: builder.mutation<AuditResponse, AuditRequest>({
			query: (body) => ({ url: "/audit", method: "POST", body }),
		}),
		// Stretch: a regrade writes new grades, so that experiment's analysis must be refetched.
		regrade: builder.mutation<RegradeResponse, RegradeRequest>({
			query: (body) => ({ url: "/regrade", method: "POST", body }),
			invalidatesTags: (_result, _error, { exp_id }) => [
				{ type: "BenchVerdict", id: exp_id },
				{ type: "BenchDiagnostics", id: exp_id },
				{ type: "BenchGrid", id: exp_id },
			],
		}),
	}),
});

export const {
	useGetHealthQuery,
	useGetDoctorQuery,
	useGetCatalogQuery,
	usePlanExperimentMutation,
	useCreateExperimentMutation,
	useStartExperimentMutation,
	useGetExperimentQuery,
	useGetVerdictQuery,
	useGetDiagnosticsQuery,
	useGetGridQuery,
	useGetRunQuery,
	useGetRunEventsQuery,
	useGetRunDiffQuery,
	useGetRunReceiptsQuery,
	useGetPairQuery,
	useAuditTableMutation,
	useRegradeMutation,
} = benchApi;