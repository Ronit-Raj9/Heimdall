import i18n from "@/lib/i18n";
import type {
	AuditRequest,
	AuditResponse,
	CatalogResponse,
	DiagnosticsResponse,
	DoctorResponse,
	ExperimentCreatedResponse,
	ExperimentListResponse,
	ExperimentResponse,
	Experiment,
	GridResponse,
	HealthResponse,
	LeaderboardParams,
	LeaderboardResponse,
	PairResponse,
	PlanResponse,
	RegradeRequest,
	RegradeResponse,
	ReleaseListResponse,
	ReleaseRequest,
	ReleaseResponse,
	RunDiffResponse,
	RunEventsResponse,
	RunReceiptsResponse,
	RunResponse,
	TriageResponse,
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

/**
 * sessionStorage key of the write token the server requires on every POST (lib.RequireWriteToken).
 * The token lives only in this tab: never in the URL, Redux or localStorage.
 */
export const WRITE_TOKEN_KEY = "bifrost-bench.write-token";

// The token for this page view when sessionStorage is blocked (private mode).
let pageWriteToken = "";

/** The write token for this tab, or "" when none was entered. Storage access is guarded. */
export function readWriteToken(): string {
	if (typeof window === "undefined") return "";
	try {
		return window.sessionStorage.getItem(WRITE_TOKEN_KEY) ?? pageWriteToken;
	} catch {
		return pageWriteToken;
	}
}

/** Stores the write token for this tab; an empty token clears it. */
export function saveWriteToken(token: string): void {
	if (typeof window === "undefined") return;
	pageWriteToken = token;
	try {
		if (token) window.sessionStorage.setItem(WRITE_TOKEN_KEY, token);
		else window.sessionStorage.removeItem(WRITE_TOKEN_KEY);
	} catch {
		// Storage blocked: pageWriteToken still carries the token for this page view.
	}
}

/** Server-sent events URL for live experiment status. Not an RTK Query endpoint: it streams. */
export function experimentEventsUrl(expId: string): string {
	return `${BENCH_API_BASE}/experiments/${encodeURIComponent(expId)}/events`;
}

/** Download URL of a release's evidence pack (a zip). Not an RTK Query endpoint: the browser saves it. */
export function releaseEvidenceUrl(releaseId: string): string {
	return `${BENCH_API_BASE}/releases/${encodeURIComponent(releaseId)}/evidence`;
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

/** JSON by default; every mutation (each a POST behind the write token) carries the token, queries never do. */
export function prepareBenchHeaders(headers: Headers, type: "query" | "mutation"): Headers {
	if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
	const token = type === "mutation" ? readWriteToken().trim() : "";
	if (token && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${token}`);
	return headers;
}

type BenchBaseQuery = BaseQueryFn<string | FetchArgs, unknown, FetchBaseQueryError>;

const httpBaseQuery: BenchBaseQuery = fetchBaseQuery({
	baseUrl: BENCH_API_BASE,
	credentials: "include",
	prepareHeaders: (headers, { type }) => prepareBenchHeaders(headers, type),
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
		"BenchRelease",
		"BenchTriage",
		"BenchLeaderboard",
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
		listExperiments: builder.query<ExperimentListResponse, void>({
			query: () => "/experiments",
			providesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
		planExperiment: builder.mutation<PlanResponse, Experiment>({
			query: (experiment) => ({ url: "/experiments/plan", method: "POST", body: experiment }),
		}),
		createExperiment: builder.mutation<ExperimentCreatedResponse, Experiment>({
			query: (experiment) => ({ url: "/experiments", method: "POST", body: experiment }),
			invalidatesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
		startExperiment: builder.mutation<ExperimentResponse, string>({
			query: (expId) => ({ url: `/experiments/${id(expId)}/start`, method: "POST" }),
			invalidatesTags: (_result, _error, expId) => [
				{ type: "BenchExperiment", id: expId },
				{ type: "BenchExperiment", id: "LIST" },
			],
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
		// Observed failure patterns per arm, read from each run's metrics.json.
		getTriage: builder.query<TriageResponse, string>({
			query: (expId) => `/experiments/${id(expId)}/triage`,
			providesTags: (_result, _error, expId) => [{ type: "BenchTriage", id: expId }],
		}),

		// Releases: the hash-chained release log.
		listReleases: builder.query<ReleaseListResponse, void>({
			query: () => "/releases",
			providesTags: [{ type: "BenchRelease", id: "LIST" }],
		}),
		getRelease: builder.query<ReleaseResponse, string>({
			query: (releaseId) => `/releases/${id(releaseId)}`,
			providesTags: (_result, _error, releaseId) => [{ type: "BenchRelease", id: releaseId }],
		}),
		createRelease: builder.mutation<ReleaseResponse, ReleaseRequest>({
			query: (body) => ({ url: "/releases", method: "POST", body }),
			invalidatesTags: [{ type: "BenchRelease", id: "LIST" }],
		}),

		// Leaderboard: agent configurations ranked by verified success across experiments. An absent
		// or empty param is left out so the server default applies (newest suite, clean, all, exclude).
		getLeaderboard: builder.query<LeaderboardResponse, LeaderboardParams | void>({
			query: (params) => {
				const search = Object.fromEntries(Object.entries(params ?? {}).filter(([, value]) => Boolean(value)));
				return Object.keys(search).length > 0 ? { url: "/leaderboard", params: search } : "/leaderboard";
			},
			providesTags: ["BenchLeaderboard"],
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
				{ type: "BenchTriage", id: exp_id },
				"BenchLeaderboard",
			],
		}),
	}),
});

export const {
	useGetHealthQuery,
	useGetDoctorQuery,
	useGetCatalogQuery,
	useListExperimentsQuery,
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
	useGetTriageQuery,
	useGetLeaderboardQuery,
	useListReleasesQuery,
	useGetReleaseQuery,
	useCreateReleaseMutation,
	useAuditTableMutation,
	useRegradeMutation,
} = benchApi;