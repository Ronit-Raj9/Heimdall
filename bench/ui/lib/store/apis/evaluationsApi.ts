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
	/** The Files API version of agents/<id>.yaml this snapshot was taken from (GET /project/versions/agents/<id>.yaml). */
	file_version?: string;
	file_sha256?: string;
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
	/** Checked automatically when the evaluation finishes. */
	gate?: EvaluationGate;
}

/** What an evaluation must show to pass. Scores are fractions (0 to 1). */
export interface EvaluationGate {
	/** The version compared with; 0 or absent: the agent's live version (the last one released). */
	baseline_version?: number;
	/** Lowest score per capability ("all" is every benchmark pooled). */
	min_score?: Record<string, number>;
	/** Largest score drop from the baseline on any capability (0.05 is 5 points). */
	max_regression?: number;
}

export type GateState = "pass" | "fail" | "no_baseline";

export interface GateCheck {
	capability: string;
	rule: "min_score" | "max_regression" | string;
	threshold: number;
	observed: number;
	baseline?: number;
	pass: boolean;
	summary: string;
}

export interface GateBaseline {
	agent: string;
	version?: number;
	arm?: string;
	evaluation_id?: string;
	live: boolean;
}

export interface EvaluationGateResult {
	state: GateState | string;
	agent: string;
	candidate: string;
	candidate_version?: number;
	baseline?: GateBaseline;
	checks: GateCheck[];
	summary: string;
	decided_at: string;
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
	/** The schedule that started this evaluation, if any. */
	schedule?: string;
	/** Set once the evaluation is done and its spec has a gate. */
	gate_result?: EvaluationGateResult;
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
	est_minutes?: Range & { known?: boolean };
	skipped?: string;
	notes?: string[] | null;
	faults_skipped?: { fault: string; reason: string }[] | null;
	[key: string]: unknown;
}

/** POST /evaluations/estimate */
export interface Estimate {
	runs: number;
	est_cost_usd: Range & { known: boolean };
	est_minutes: Range & { known?: boolean };
	per_benchmark: EstimateBenchmark[];
	notes?: string[] | null;
}

// ---------------------------------------------------------------------------------------------
// 4. Report card (A04, A05)
// ---------------------------------------------------------------------------------------------

export type ReportCapabilityId = "terminal" | "coding" | "memory" | "support" | "research" | "voice" | "safety" | "team" | "gpu";

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

/** A/B verdict on one benchmark (bench/verdictab). Winner is an arm label, "tie" or "unclear". */
export interface ReportVerdict {
	winner: string;
	delta: number;
	significant: boolean;
	summary: string;
	p_value?: number;
	/** p_value after Holm across the evaluation's benchmarks; significant is read from this. */
	p_value_holm?: number;
	ci_low?: number;
	ci_high?: number;
	tasks?: number;
}

export interface ReportCapabilityRow {
	capability: ReportCapabilityId | string;
	benchmarks: string[];
	arms: Record<string, ReportMetrics>;
}

export interface ReportBenchmarkRow {
	benchmark: string;
	capability: ReportCapabilityId | string;
	experiment_id: string;
	arms: Record<string, ReportMetrics>;
	verdict?: ReportVerdict;
}

export interface ReportReliabilityRow {
	fault: string;
	benchmarks?: string[];
	arms: Record<string, { score: number; drop: number; runs?: number }>;
}

export interface ReportSafety {
	attack_resisted: Record<string, number>;
	cheat_rate: Record<string, number>;
}

export interface ReportOverall {
	winner: string;
	wins: Record<string, number>;
	summary: string;
	delta?: number;
	significant?: boolean;
	p_value?: number;
}

/** GET /evaluations/{id}/report and GET /agents/{id}/report-card */
export interface Report {
	evaluation: string;
	status?: string;
	/** The evaluation's mode. "score" shows one arm and never an A/B verdict. */
	mode?: EvaluationMode | string;
	arms: ReportArm[];
	capabilities: ReportCapabilityRow[];
	benchmarks: ReportBenchmarkRow[];
	reliability: ReportReliabilityRow[];
	safety?: ReportSafety;
	/** Only for A/B modes. */
	overall?: ReportOverall;
	/** Per arm label, over every benchmark. */
	totals?: Record<string, ReportTotals>;
}

/** One arm's numbers over the whole evaluation; absent fields are unknown. */
export interface ReportTotals {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
	solved: number;
	cost_usd: number;
	cost_per_solved_usd?: number;
	false_completion?: number;
	fault_survival?: number;
	fault_runs?: number;
}

// Names other lanes use for the same shapes.
export type ReportCardResponse = Report;
export type EvaluationReport = Report;
export type ReportArmMetrics = ReportMetrics;
export type ReportCapability = ReportCapabilityRow;
export type ReportBenchmark = ReportBenchmarkRow;
export type ReportReliability = ReportReliabilityRow;
export type AgentCompatibility = Compatibility;
export type BenchmarkCompatibility = CompatibilityBenchmark;
export type EvaluationPack = Pack;
export type EvaluationEstimate = Estimate;
export type EvaluationRange = Range;
export type EstimateRange = Range;
export type AgentVersionsResponse = AgentVersionList;

export interface AgentReportCardParams {
	agent: string;
	version?: number;
	model?: string;
}

/** The agent report card request: version and model only when set, so the server picks the latest. */
export function agentReportCardRequest({ agent, version, model }: AgentReportCardParams): { url: string; params?: Record<string, string> } {
	const params: Record<string, string> = {};
	if (version) params.version = String(version);
	if (model) params.model = model;
	const url = `/agents/${encodeURIComponent(agent)}/report-card`;
	return Object.keys(params).length > 0 ? { url, params } : { url };
}

/** The list answers either a bare array or {evaluations: [...]}; both become an array. */
export function normalizeEvaluationList(raw: unknown): Evaluation[] {
	if (Array.isArray(raw)) return raw as Evaluation[];
	if (raw && typeof raw === "object" && Array.isArray((raw as { evaluations?: unknown }).evaluations)) {
		return (raw as { evaluations: Evaluation[] }).evaluations;
	}
	return [];
}

// The agent leaderboard (A17) lives in ./agentLeaderboardApi; its types are re-exported here.
export type { AgentCapabilityScore as AgentLeaderboardCell, AgentLeaderboardRow, AgentLeaderboardResponse } from "./agentLeaderboardApi";
export { useGetAgentLeaderboardQuery } from "./agentLeaderboardApi";

/** Built-in library agents marked baseline on the agent leaderboard. */
export const BASELINE_AGENTS = ["codex", "claude-code", "mini-swe-agent", "opencode", "gemini-cli"];

// ---------------------------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------------------------

const enc = encodeURIComponent;


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
				transformResponse: (body: unknown) => normalizeEvaluationList(body),
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
			/** The same estimate as a query, so a form can show it live (cached per spec). */
			getEvaluationEstimate: builder.query<Estimate, EvaluationSpec>({
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
				query: (params) => agentReportCardRequest(params),
				providesTags: (_r, _e, { agent }) => [{ type: "Evaluations", id: `card/${agent}` }],
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
	useGetEvaluationEstimateQuery,
} = evaluationsApi;

// Hook names other lanes use.
export const useGetEvaluationsQuery = useListEvaluationsQuery;
export const useCreateAgentVersionMutation = useSnapshotAgentVersionMutation;
export const useEstimateEvaluationQuery = useGetEvaluationEstimateQuery;
