import i18n from "@/lib/i18n";
import type {
	AgentsResponse,
	CatalogResponse,
	ClusterDetailResponse,
	ClusterListResponse,
	ClusterParams,
	DiagnosticsResponse,
	ExperimentCreatedResponse,
	ExperimentListResponse,
	ExperimentResponse,
	Experiment,
	GridResponse,
	HealthResponse,
	ImportsResponse,
	LeaderboardParams,
	LeaderboardResponse,
	ForecastResponse,
	ModelMatrixParams,
	ModelMatrixResponse,
	PairResponse,
	PlanResponse,
	RegradeRequest,
	RegradeResponse,
	ReleaseListResponse,
	ReleaseRequest,
	ReleaseResponse,
	ReviewQueueResponse,
	ReviewRequest,
	ReviewResponse,
	RunDiffResponse,
	RunEventsResponse,
	RunReceiptsResponse,
	RunResponse,
	RunReviewsResponse,
	RunList,
	RunListParams,
	SuiteTaskResponse,
	SuiteHealthResponse,
	SuitesResponse,
	TriageResponse,
	VerdictResponse,
	WorkersResponse,
} from "@/lib/types/bench";
import { createApi, fetchBaseQuery, type BaseQueryFn, type FetchArgs, type FetchBaseQueryError } from "@reduxjs/toolkit/query/react";
import { BENCH_API_BASE, FIXTURE_PAIRS, detectFixtureMode, fixtureRefusal, resolveFixture, scopeFixture } from "./fixtures";
import { CSRF_HEADER, errorStatus, getAuthState, notifyUnauthorized, ORG_HEADER, whenAuthKnown } from "./session";

/** Polling interval for experiment status when server-sent events are unavailable (DESIGN.md 16.7). */
export const EXPERIMENT_POLL_MS = 2000;

/**
 * Decided once at load, so the flag survives in-app navigation that drops the query string.
 * Always false in a production build: __BENCH_FIXTURES__ is the literal false there, so the
 * fixture base query below is dead code the bundler drops.
 */
export const IS_FIXTURE_MODE = __BENCH_FIXTURES__ && detectFixtureMode(
	typeof window === "undefined" ? "" : window.location.search,
	import.meta.env.VITE_BENCH_FIXTURE,
);

/**
 * Whether a pair can be opened: always against a server; in fixture mode only the pairs with a
 * fixture file, so grid cells and picker buttons never lead to a missing fixture (QC2-29-04).
 */
export function pairOpenable(pairId: string, fixtureMode: boolean = IS_FIXTURE_MODE): boolean {
	return !fixtureMode || FIXTURE_PAIRS.includes(pairId);
}

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

/**
 * JSON by default. Local mode: every mutation (each a POST behind the write token) carries the
 * token, queries never do. Hosted mode: the session cookie authenticates, so no token is sent;
 * every mutation carries the session's CSRF value instead, and every request names the org this
 * tab displays (X-Heimdall-Org) so a switch in another tab cannot redirect it.
 */
export function prepareBenchHeaders(headers: Headers, type: "query" | "mutation"): Headers {
	if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
	const auth = getAuthState();
	if (auth.mode === "hosted") {
		if (type === "mutation" && auth.csrf && !headers.has(CSRF_HEADER)) headers.set(CSRF_HEADER, auth.csrf);
		if (auth.org && !headers.has(ORG_HEADER)) headers.set(ORG_HEADER, auth.org);
		return headers;
	}
	const token = type === "mutation" ? readWriteToken().trim() : "";
	if (token && !headers.has("Authorization")) headers.set("Authorization", `Bearer ${token}`);
	return headers;
}

/**
 * A request that writes (any method but GET or HEAD) carries the write credentials even when it is
 * declared as a query: POST /evaluations/estimate is a query so a form can show it live, but the
 * server checks it as a write (CSRF in hosted mode, the write token locally).
 */
export function writeType(type: "query" | "mutation", arg: unknown): "query" | "mutation" {
	if (type === "mutation") return type;
	const method = arg && typeof arg === "object" && "method" in arg ? String((arg as { method?: unknown }).method ?? "GET") : "GET";
	return method.toUpperCase() === "GET" || method.toUpperCase() === "HEAD" ? "query" : "mutation";
}

type BenchBaseQuery = BaseQueryFn<string | FetchArgs, unknown, FetchBaseQueryError>;

const fetchBenchQuery: BenchBaseQuery = fetchBaseQuery({
	baseUrl: BENCH_API_BASE,
	credentials: "include",
	prepareHeaders: (headers, { type }) => prepareBenchHeaders(headers, type),
});

// A hosted 401 outside /auth/* means the session ended: the session provider sends the visitor to
// the login page. In local mode a 401 is the write-token check and stays on the page.
// The write check reads the resolved request (args), not the endpoint's argument: prepareHeaders
// only sees the latter, which for a POST query is the request body.
const httpBaseQuery: BenchBaseQuery = async (args, api, extraOptions) => {
	const type = writeType(api.type, args);
	const url = typeof args === "string" ? args : args.url;
	if (type === "mutation" && !url.startsWith("/auth/")) await whenAuthKnown();
	const result = await fetchBenchQuery(args, type === api.type ? api : { ...api, type }, extraOptions);
	if (result.error && errorStatus(result.error) === 401) notifyUnauthorized(typeof args === "string" ? args : args.url);
	if (result.error && errorStatus(result.error) === 429) {
		return { ...result, error: rateLimitedError((result.meta as { response?: Response } | undefined)?.response?.headers.get("Retry-After") ?? null) };
	}
	return result;
};

/**
 * A 429 answer with its message in plain words: "Too many requests. Try again in N seconds." The
 * wait comes from Retry-After (seconds); without one the message leaves the number out.
 */
export function rateLimitedError(retryAfter: string | null): FetchBaseQueryError {
	const seconds = retryAfter !== null && /^\d+$/.test(retryAfter.trim()) ? Number(retryAfter.trim()) : null;
	const message =
		seconds !== null
			? i18n.t("errors.rateLimited", { ns: "bench", count: Math.max(1, seconds) })
			: i18n.t("errors.rateLimitedNoWait", { ns: "bench" });
	return { status: 429, data: { error: { message } } } as FetchBaseQueryError;
}

const fixtureBaseQuery: BenchBaseQuery = async (args) => {
	const url = typeof args === "string" ? args : args.url;
	const method = typeof args === "string" ? "GET" : (args.method ?? "GET");
	const params = typeof args === "string" ? undefined : (args.params as Record<string, unknown> | undefined);
	const refusal = fixtureRefusal(method, url, params);
	if (refusal) return { error: { status: refusal.status, data: { error: { message: refusal.message } } } };
	const file = resolveFixture(method, url);
	if (!file) {
		return { error: { status: 404, data: { error: { message: i18n.t("errors.noFixture", { ns: "bench", method, url }) } } } };
	}
	try {
		const response = await fetch(`${import.meta.env.BASE_URL}fixtures/${file}`);
		if (!response.ok) {
			return { error: { status: response.status, data: { error: { message: i18n.t("errors.fixtureLoad", { ns: "bench", file }) } } } };
		}
		return { data: scopeFixture(method, url, params, (await response.json()) as unknown) };
	} catch (error) {
		return { error: { status: "FETCH_ERROR", error: String(error) } };
	}
};

const id = (value: string) => encodeURIComponent(value);

/** The verdict query's argument when it names a grade source. */
export interface VerdictArgs {
	expId: string;
	/** original, a regrade id, or reviewed; absent takes the server default (original). */
	gradeSource?: string;
}

/** The verdict query's argument in object form. */
export function verdictArgs(arg: string | VerdictArgs): VerdictArgs {
	return typeof arg === "string" ? { expId: arg } : arg;
}

/**
 * True when GET /workers says worker mode is off rather than broken: the list answers enabled
 * false (BENCH_WORKER_TOKEN unset), or a server without the route answers 404.
 */
export function isWorkersOff(error: unknown, data?: { enabled?: boolean }): boolean {
	if (data && data.enabled === false) return true;
	return Boolean(error && typeof error === "object" && "status" in error && (error as { status: unknown }).status === 404);
}

/** GET /workers with a missing or null list read as empty, so the table never has to tell null from none. */
export function normalizeWorkers(raw: unknown): WorkersResponse {
	const body = (raw ?? {}) as WorkersResponse;
	return { ...body, workers: Array.isArray(body.workers) ? body.workers : [] };
}

/**
 * GET /suites/{id}/health with null lists and maps (Go nil) read as empty, so the suite page never
 * has to tell null from none.
 */
export function normalizeSuiteHealth(raw: unknown): SuiteHealthResponse {
	const body = (raw ?? {}) as SuiteHealthResponse;
	const list = <T>(value: T[] | null | undefined): T[] => (Array.isArray(value) ? value : []);
	return {
		...body,
		counts: body.counts && typeof body.counts === "object" ? body.counts : {},
		notes: list(body.notes),
		tasks: list(body.tasks).map((task) => ({
			...task,
			quarantine: task.quarantine ?? null,
			flags: list(task.flags),
			verifier: { ...task.verifier, evidence: list(task.verifier?.evidence) },
			variance: { ...task.variance, per_config: list(task.variance?.per_config) },
		})),
	};
}

/**
 * GET /catalog with null lists (a server before the Go fix, or a bench root without an agents/
 * folder) read as empty, so the new experiment form never reads a null list.
 */
export function normalizeCatalog(raw: unknown): CatalogResponse {
	const body = (raw ?? {}) as CatalogResponse;
	const list = <T>(value: T[] | null | undefined): T[] => (Array.isArray(value) ? value : []);
	return {
		...body,
		suites: list(body.suites).map((suite) => ({ ...suite, tasks: list(suite.tasks), probes: list(suite.probes) })),
		probes: list(body.probes),
		agents: list(body.agents),
		metrics: list(body.metrics),
		gates: list(body.gates),
		parts: body.parts && typeof body.parts === "object" ? body.parts : {},
	};
}

/** A clusters request: empty, zero and absent params are left out so the server default applies. */
export function clusterRequest(url: string, params: ClusterParams): string | FetchArgs {
	const search = Object.fromEntries(Object.entries(params).filter(([, value]) => Boolean(value)));
	return Object.keys(search).length > 0 ? { url, params: search } : url;
}

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
		"BenchSuites",
		"BenchAgents",
		"BenchImports",
		"BenchWorkers",
		"BenchReviews",
		"BenchMatrix",
		"BenchClusters",
		// Per-experiment derived results refreshed when a run settles (QC2-27-02): migration, cost,
		// Pareto (migrationApi.ts) and the adaptive, support, research and routing summaries.
		"BenchMigration",
		"BenchCostExplain",
		"BenchPareto",
		"BenchExperimentResult",
		// Hosted platform (platformApi.ts)
		"AuthMe",
		"Members",
		"Invitations",
		"ApiTokens",
		"Runners",
		"RunnerTokens",
		"AuditLog",
		"Jobs",
	],
	endpoints: (builder) => ({
		getHealth: builder.query<HealthResponse, void>({
			query: () => "/health",
			providesTags: ["BenchHealth"],
		}),
		getCatalog: builder.query<CatalogResponse, void>({
			query: () => "/catalog",
			transformResponse: (raw: unknown) => normalizeCatalog(raw),
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
		// Expected cost and time from sealed history; same body and write token as the plan. Writes nothing.
		estimateExperiment: builder.mutation<ForecastResponse, Experiment>({
			query: (experiment) => ({ url: "/estimate", method: "POST", body: experiment }),
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
		// A string asks for the default grade source; an object may name one (original, a regrade id, reviewed).
		getVerdict: builder.query<VerdictResponse, string | VerdictArgs>({
			query: (arg) => {
				const { expId, gradeSource } = verdictArgs(arg);
				const url = `/experiments/${id(expId)}/verdict`;
				return gradeSource ? { url, params: { grade_source: gradeSource } } : url;
			},
			providesTags: (_result, _error, arg) => [{ type: "BenchVerdict", id: verdictArgs(arg).expId }],
		}),
		// U2 UI regroup: like the verdict, an object may name a grade source, so both read the same grades.
		getDiagnostics: builder.query<DiagnosticsResponse, string | VerdictArgs>({
			query: (arg) => {
				const { expId, gradeSource } = verdictArgs(arg);
				const url = `/experiments/${id(expId)}/diagnostics`;
				return gradeSource ? { url, params: { grade_source: gradeSource } } : url;
			},
			providesTags: (_result, _error, arg) => [{ type: "BenchDiagnostics", id: verdictArgs(arg).expId }],
		}),
		// Every arm on one condition with every pair tested. An absent or empty param uses the server default.
		getModelMatrix: builder.query<ModelMatrixResponse, ModelMatrixParams>({
			query: ({ expId, ...params }) => {
				const search = Object.fromEntries(Object.entries(params).filter(([, value]) => Boolean(value)));
				const url = `/experiments/${id(expId)}/matrix`;
				return Object.keys(search).length > 0 ? { url, params: search } : url;
			},
			providesTags: (_result, _error, { expId }) => [{ type: "BenchMatrix", id: expId }],
		}),
		getGrid: builder.query<GridResponse, string>({
			query: (expId) => `/experiments/${id(expId)}/grid`,
			providesTags: (_result, _error, expId) => [{ type: "BenchGrid", id: expId }],
		}),

		// Runs and pairs
		// U2 UI regroup: every run across experiments, one page, newest first. Empty filters are left out.
		listRuns: builder.query<RunList, RunListParams | void>({
			query: (params) => {
				const search = Object.fromEntries(
					Object.entries(params ?? {}).filter(([, value]) => value !== undefined && value !== null && value !== ""),
				);
				return Object.keys(search).length > 0 ? { url: "/runs", params: search } : "/runs";
			},
			providesTags: [{ type: "BenchRun", id: "LIST" }],
		}),
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
		// Failure clusters across experiments; one cluster and its runs. Empty params take the server default.
		getClusters: builder.query<ClusterListResponse, ClusterParams | void>({
			query: (params) => clusterRequest("/clusters", params ?? {}),
			providesTags: [{ type: "BenchClusters", id: "LIST" }],
		}),
		getCluster: builder.query<ClusterDetailResponse, ClusterParams & { signature: string }>({
			query: ({ signature, ...params }) => clusterRequest(`/clusters/${id(signature)}`, params),
			providesTags: (_result, _error, { signature }) => [{ type: "BenchClusters", id: signature }],
		}),

		// Releases: the hash-chained release log.
		listReleases: builder.query<ReleaseListResponse, void>({
			query: () => "/releases",
			providesTags: [{ type: "BenchRelease", id: "LIST" }],
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

		// Suites and agents: the benchmark packs and agent configs on disk, read-only. Hidden tests,
		// solutions, controls and environment values are never in these payloads.
		getSuites: builder.query<SuitesResponse, void>({
			query: () => "/suites",
			providesTags: [{ type: "BenchSuites", id: "LIST" }],
		}),
		getSuiteTask: builder.query<SuiteTaskResponse, { suite: string; task: string }>({
			query: ({ suite, task }) => `/suites/${id(suite)}/tasks/${id(task)}`,
			providesTags: (_result, _error, { suite, task }) => [{ type: "BenchSuites", id: `${suite}/${task}` }],
		}),
		// Suite health from sealed runs across experiments. 503 when the server has no run store.
		getSuiteHealth: builder.query<SuiteHealthResponse, string>({
			query: (suite) => `/suites/${id(suite)}/health`,
			transformResponse: (raw: unknown) => normalizeSuiteHealth(raw),
			providesTags: (_result, _error, suite) => [{ type: "BenchSuites", id: `${suite}/health` }],
		}),
		getAgents: builder.query<AgentsResponse, void>({
			query: () => "/agents",
			providesTags: ["BenchAgents"],
		}),
		// Importers the server links: what each converts and what a run of it needs. Read-only.
		getImports: builder.query<ImportsResponse, void>({
			query: () => "/imports",
			providesTags: ["BenchImports"],
		}),
		// Remote workers. enabled false, or a 404 from a server without the route, means worker mode is off (isWorkersOff).
		getWorkers: builder.query<WorkersResponse, void>({
			query: () => "/workers",
			transformResponse: (raw: unknown) => normalizeWorkers(raw),
			providesTags: ["BenchWorkers"],
		}),

		// Human review: the queue of runs to look at, the hash-chained review log, one run's reviews,
		// and posting a review (a write: write token, refused in recorded and synthetic mode).
		getReviewQueue: builder.query<ReviewQueueResponse, { expId: string; reason?: string }>({
			query: ({ expId, reason }) => {
				const url = `/experiments/${id(expId)}/reviews/queue`;
				return reason ? { url, params: { reason } } : url;
			},
			providesTags: (_result, _error, { expId }) => [{ type: "BenchReviews", id: `queue:${expId}` }],
		}),
		getRunReviews: builder.query<RunReviewsResponse, string>({
			query: (runId) => `/runs/${id(runId)}/reviews`,
			providesTags: (_result, _error, runId) => [{ type: "BenchReviews", id: `run:${runId}` }],
		}),
		createReview: builder.mutation<ReviewResponse, ReviewRequest>({
			query: (body) => ({ url: "/reviews", method: "POST", body }),
			invalidatesTags: (result, _error, arg) => reviewInvalidatedTags(result?.exp_id ?? arg.exp_id, arg.run_id),
		}),

		// Analysis
		// A regrade runs as an experiment.regate job (handlers/experimentjobs.go): 202 with the queued
		// job. The original grades stay; once the job succeeds, ?grade=<regrade id> on the verdict
		// reads the new ones, so nothing cached is stale yet. The jobs list picks the job up.
		regrade: builder.mutation<RegradeResponse, RegradeRequest>({
			query: ({ exp_id, ...body }) => ({ url: `/experiments/${id(exp_id)}/regrade`, method: "POST", body }),
			invalidatesTags: [{ type: "Jobs", id: "LIST" }],
		}),
	}),
});

export const {
	useGetHealthQuery,
	useGetCatalogQuery,
	useListExperimentsQuery,
	usePlanExperimentMutation,
	useEstimateExperimentMutation,
	useGetModelMatrixQuery,
	useCreateExperimentMutation,
	useStartExperimentMutation,
	useGetExperimentQuery,
	useGetVerdictQuery,
	useGetDiagnosticsQuery,
	useGetGridQuery,
	useListRunsQuery,
	useGetRunQuery,
	useGetRunEventsQuery,
	useGetRunDiffQuery,
	useGetRunReceiptsQuery,
	useGetPairQuery,
	useGetTriageQuery,
	useGetClustersQuery,
	useGetClusterQuery,
	useGetLeaderboardQuery,
	useGetSuitesQuery,
	useGetSuiteTaskQuery,
	useGetSuiteHealthQuery,
	useGetAgentsQuery,
	useGetImportsQuery,
	useGetWorkersQuery,
	useListReleasesQuery,
	useCreateReleaseMutation,
	useRegradeMutation,
	useGetReviewQueueQuery,
	useGetRunReviewsQuery,
	useCreateReviewMutation,
} = benchApi;
/**
 * The caches a human review changes. Every analysis slice that consumes reviews refreshes together,
 * so a reviewed verdict never shows next to diagnostics from before the review.
 */
export function reviewInvalidatedTags(expId: string | undefined, runId: string) {
	return [
		{ type: "BenchReviews" as const, id: `run:${runId}` },
		...(expId
			? [
					{ type: "BenchReviews" as const, id: `queue:${expId}` },
					{ type: "BenchReviews" as const, id: `log:${expId}` },
					{ type: "BenchVerdict" as const, id: expId },
					{ type: "BenchDiagnostics" as const, id: expId },
				]
			: []),
		"BenchLeaderboard" as const,
	];
}

/**
 * The result caches of one experiment that change while it runs: refreshed once when it stops
 * running, so the header's Done never sits next to a partial verdict, grid or run list.
 */
export function experimentResultTags(expId: string) {
	return [
		{ type: "BenchVerdict" as const, id: expId },
		{ type: "BenchDiagnostics" as const, id: expId },
		{ type: "BenchGrid" as const, id: expId },
		{ type: "BenchMatrix" as const, id: expId },
		{ type: "BenchTriage" as const, id: expId },
		{ type: "BenchRun" as const, id: "LIST" },
		{ type: "BenchMigration" as const, id: expId },
		{ type: "BenchCostExplain" as const, id: expId },
		{ type: "BenchPareto" as const, id: expId },
		{ type: "BenchExperimentResult" as const, id: expId },
	];
}
