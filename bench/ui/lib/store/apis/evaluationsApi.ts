import { benchApi } from "./benchApi";

/**
 * Agent-first benchmarking (audit/AGENTFIRST_CONTRACT.md, lanes A01-A19): capabilities, agent
 * versions, evaluations, packs, estimates, report cards and the agent leaderboard. The types mirror
 * the Go JSON exactly; other UI lanes import from here.
 */

// ---------------------------------------------------------------------------------------------
// 1. Capabilities (A01)
// ---------------------------------------------------------------------------------------------

export type Capability = "shell" | "tools" | "mcp" | "sessions" | "browser" | "desktop" | "voice" | "gpu";
export const CAPABILITIES: Capability[] = ["shell", "tools", "mcp", "sessions", "browser", "desktop", "voice", "gpu"];

export interface CompatibilityBenchmark {
	id: string;
	ok: boolean;
	requires: Capability[];
	missing: Capability[];
	reason: string;
}

/** GET /agents/{id}/compatibility */
export interface Compatibility {
	agent: string;
	capabilities: Capability[];
	benchmarks: CompatibilityBenchmark[];
}

// ---------------------------------------------------------------------------------------------
// 2. Agent versions (A06)
// ---------------------------------------------------------------------------------------------

export interface AgentVersion {
	version: number;
	hash: string;
	created_at: string;
	created_by: string;
	note: string;
	/** Runs made with this version; present on the list endpoint. */
	runs?: number;
}

/** GET /agents/{id}/versions */
export interface AgentVersionList {
	agent: string;
	versions: AgentVersion[];
}

/** GET /agents/{id}/versions/{n} */
export interface AgentVersionDetail {
	version: number;
	hash: string;
	yaml: string;
}

// ---------------------------------------------------------------------------------------------
// 3. Evaluations (A02, A03, A07)
// ---------------------------------------------------------------------------------------------

export type EvaluationMode = "score" | "versions" | "models" | "opponent";
export type EvaluationStatus = "queued" | "running" | "done" | "failed" | "cancelled";
export type EvaluationFault = "clean" | "model-down" | "broken-tool" | "misleading-instruction";
export type PackId = "coding" | "terminal" | "support" | "memory" | "research" | "voice" | "safety" | "all-compatible";

export interface EvaluationArm {
	label: string;
	agent: string;
	version?: number;
	model: string;
	provider?: string;
}

export interface EvaluationSpec {
	name: string;
	mode: EvaluationMode;
	/** 1 arm for score, 2 for A/B. */
	arms: EvaluationArm[];
	/** Suite ids; or Pack. */
	benchmarks: string[];
	pack?: PackId | string;
	/** Default 5, admitted tasks only. */
	tasks_per_benchmark: number;
	/** Repeats per task, default 2. */
	trials: number;
	/** "clean" always; plus "model-down", "broken-tool", "misleading-instruction". */
	faults: (EvaluationFault | string)[];
	budget_usd?: number;
}

export interface EvaluationChild {
	benchmark: string;
	experiment_id: string;
	status: string;
	skipped?: string;
}

export interface Evaluation {
	id: string;
	org?: string;
	spec: EvaluationSpec;
	status: EvaluationStatus;
	children: EvaluationChild[];
	created_at: string;
	created_by?: string;
}

/** GET /evaluation-packs item. */
export interface Pack {
	id: PackId | string;
	name: string;
	benchmarks: string[];
	capability: string;
}

export interface Range {
	low: number;
	high: number;
}

export interface EstimateBenchmark {
	benchmark: string;
	runs?: number;
	est_cost_usd?: Range & { known?: boolean };
	est_minutes?: Range;
	skipped?: string;
	[key: string]: unknown;
}

/** POST /evaluations/estimate */
export interface Estimate {
	runs: number;
	est_cost_usd: Range & { known: boolean };
	est_minutes: Range;
	per_benchmark: EstimateBenchmark[];
}

// ---------------------------------------------------------------------------------------------
// 4. Report card (A04, A05)
// ---------------------------------------------------------------------------------------------

export type ReportCapability = "terminal" | "coding" | "memory" | "support" | "research" | "voice" | "safety" | "team" | "gpu";

export interface ReportMetrics {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
	cost_usd?: number;
	tokens?: number;
	steps?: number;
	tool_calls?: number;
	wall_s?: number;
}

export interface ReportArm {
	label: string;
	agent: string;
	version?: number;
	model: string;
}

export interface ReportVerdict {
	winner: string;
	delta: number;
	significant: boolean;
	summary: string;
}

export interface ReportCapabilityRow {
	capability: ReportCapability | string;
	benchmarks: string[];
	arms: Record<string, ReportMetrics>;
}

export interface ReportBenchmarkRow {
	benchmark: string;
	capability: ReportCapability | string;
	experiment_id: string;
	arms: Record<string, ReportMetrics>;
	verdict?: ReportVerdict;
}

export interface ReportReliabilityRow {
	fault: string;
	arms: Record<string, { score: number; drop: number }>;
}

export interface ReportSafety {
	attack_resisted?: Record<string, number>;
	cheat_rate?: Record<string, number>;
}

export interface ReportOverall {
	winner: string;
	wins: Record<string, number>;
	summary: string;
}

/** GET /evaluations/{id}/report and GET /agents/{id}/report-card */
export interface Report {
	evaluation: string;
	arms: ReportArm[];
	capabilities: ReportCapabilityRow[];
	benchmarks: ReportBenchmarkRow[];
	reliability: ReportReliabilityRow[];
	safety: ReportSafety;
	overall: ReportOverall;
}

export interface AgentLeaderboardCell {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
}

/** GET /leaderboard/agents row. */
export interface AgentLeaderboardRow {
	agent: string;
	version: number;
	model: string;
	label: string;
	baseline: boolean;
	capabilities: Record<string, AgentLeaderboardCell>;
	cost_usd: number;
	last_evaluation: string;
}

/** Built-in library agents marked baseline on the agent leaderboard. */
export const BASELINE_AGENTS = ["codex", "claude-code", "mini-swe-agent", "opencode", "gemini-cli"];

// ---------------------------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------------------------

const enc = encodeURIComponent;

function qs(params: Record<string, string | number | undefined | null>): string {
	const p = new URLSearchParams();
	for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== "") p.set(k, String(v));
	const s = p.toString();
	return s ? `?${s}` : "";
}

export const evaluationsApi = benchApi
	.enhanceEndpoints({ addTagTypes: ["Evaluations", "AgentVersions", "AgentCompat", "AgentLeaderboard"] })
	.injectEndpoints({
		endpoints: (builder) => ({
			getAgentCompatibility: builder.query<Compatibility, string>({
				query: (agent) => `/agents/${enc(agent)}/compatibility`,
				providesTags: (_r, _e, agent) => [{ type: "AgentCompat", id: agent }],
			}),
			getAgentVersions: builder.query<AgentVersionList, string>({
				query: (agent) => `/agents/${enc(agent)}/versions`,
				providesTags: (_r, _e, agent) => [{ type: "AgentVersions", id: agent }],
			}),
			getAgentVersion: builder.query<AgentVersionDetail, { agent: string; version: number }>({
				query: ({ agent, version }) => `/agents/${enc(agent)}/versions/${version}`,
				providesTags: (_r, _e, { agent, version }) => [{ type: "AgentVersions", id: `${agent}@${version}` }],
			}),
			snapshotAgentVersion: builder.mutation<AgentVersion, { agent: string; note?: string }>({
				query: ({ agent, note }) => ({ url: `/agents/${enc(agent)}/versions`, method: "POST", body: { note: note ?? "" } }),
				invalidatesTags: (_r, _e, { agent }) => [{ type: "AgentVersions", id: agent }],
			}),
			listEvaluations: builder.query<Evaluation[], void>({
				query: () => "/evaluations",
				transformResponse: (body: Evaluation[] | { evaluations?: Evaluation[] | null } | null) =>
					Array.isArray(body) ? body : (body?.evaluations ?? []),
				providesTags: [{ type: "Evaluations", id: "LIST" }],
			}),
			getEvaluation: builder.query<Evaluation, string>({
				query: (id) => `/evaluations/${enc(id)}`,
				providesTags: (_r, _e, id) => [{ type: "Evaluations", id }],
			}),
			createEvaluation: builder.mutation<Evaluation, EvaluationSpec>({
				query: (body) => ({ url: "/evaluations", method: "POST", body }),
				invalidatesTags: [
					{ type: "Evaluations", id: "LIST" },
					{ type: "BenchExperiment", id: "LIST" },
				],
			}),
			cancelEvaluation: builder.mutation<Evaluation, string>({
				query: (id) => ({ url: `/evaluations/${enc(id)}/cancel`, method: "POST" }),
				invalidatesTags: (_r, _e, id) => [
					{ type: "Evaluations", id },
					{ type: "Evaluations", id: "LIST" },
				],
			}),
			estimateEvaluation: builder.mutation<Estimate, EvaluationSpec>({
				query: (body) => ({ url: "/evaluations/estimate", method: "POST", body }),
			}),
			getEvaluationPacks: builder.query<Pack[], void>({
				query: () => "/evaluation-packs",
				transformResponse: (body: Pack[] | { packs?: Pack[] | null } | null) =>
					Array.isArray(body) ? body : (body?.packs ?? []),
			}),
			getEvaluationReport: builder.query<Report, string>({
				query: (id) => `/evaluations/${enc(id)}/report`,
				providesTags: (_r, _e, id) => [{ type: "Evaluations", id: `${id}/report` }],
			}),
			getAgentReportCard: builder.query<Report, { agent: string; version?: number; model?: string }>({
				query: ({ agent, version, model }) => `/agents/${enc(agent)}/report-card${qs({ version, model })}`,
				providesTags: (_r, _e, { agent }) => [{ type: "Evaluations", id: `card/${agent}` }],
			}),
			getAgentLeaderboard: builder.query<AgentLeaderboardRow[], { capability?: string; model?: string } | void>({
				query: (args) => `/leaderboard/agents${qs({ capability: args?.capability, model: args?.model })}`,
				transformResponse: (body: AgentLeaderboardRow[] | { rows?: AgentLeaderboardRow[] | null } | null) =>
					Array.isArray(body) ? body : (body?.rows ?? []),
				providesTags: [{ type: "AgentLeaderboard", id: "LIST" }],
			}),
		}),
	});

export const {
	useGetAgentCompatibilityQuery,
	useGetAgentVersionsQuery,
	useGetAgentVersionQuery,
	useSnapshotAgentVersionMutation,
	useListEvaluationsQuery,
	useGetEvaluationQuery,
	useCreateEvaluationMutation,
	useCancelEvaluationMutation,
	useEstimateEvaluationMutation,
	useGetEvaluationPacksQuery,
	useGetEvaluationReportQuery,
	useGetAgentReportCardQuery,
	useGetAgentLeaderboardQuery,
} = evaluationsApi;
