import { benchApi } from "./benchApi";

/**
 * Agent-first benchmarking (audit/AGENTFIRST_CONTRACT.md): evaluations, packs, estimates, report
 * cards, agent versions, agent compatibility and the agent leaderboard. Types mirror the contract
 * JSON exactly so every UI lane imports them from here.
 */

const enc = encodeURIComponent;

// 1. Capabilities

export type Capability = "shell" | "tools" | "mcp" | "sessions" | "browser" | "desktop" | "voice" | "gpu";

export interface BenchmarkCompatibility {
	id: string;
	ok: boolean;
	requires: Capability[];
	missing: Capability[];
	reason: string;
}

export interface AgentCompatibility {
	agent: string;
	capabilities: Capability[];
	benchmarks: BenchmarkCompatibility[];
}

// 2. Agent versions

export interface AgentVersion {
	version: number;
	hash: string;
	created_at: string;
	created_by: string;
	note: string;
	runs?: number;
}

export interface AgentVersionsResponse {
	agent: string;
	versions: AgentVersion[];
}

export interface AgentVersionDetail {
	version: number;
	hash: string;
	yaml: string;
}

// 3. Evaluations

export type EvaluationMode = "score" | "versions" | "models" | "opponent";
export type EvaluationStatus = "queued" | "running" | "done" | "failed" | "cancelled";
export type EvaluationFault = "clean" | "model-down" | "broken-tool" | "misleading-instruction";

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
	arms: EvaluationArm[];
	benchmarks: string[];
	pack?: string;
	tasks_per_benchmark: number;
	trials: number;
	faults: string[];
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

export interface EvaluationPack {
	id: string;
	name: string;
	benchmarks: string[];
	capability: string;
}

export interface EstimateRange {
	low: number;
	high: number;
}

export interface EvaluationEstimate {
	runs: number;
	est_cost_usd: EstimateRange & { known: boolean };
	est_minutes: EstimateRange;
	per_benchmark: Array<Record<string, unknown>>;
}

// 4. Report card

export interface ReportArm {
	label: string;
	agent: string;
	version?: number;
	model: string;
}

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

export interface ReportCapability {
	capability: string;
	benchmarks: string[];
	arms: Record<string, ReportMetrics>;
}

export interface ReportVerdict {
	winner: string;
	delta: number;
	significant: boolean;
	summary: string;
}

export interface ReportBenchmark {
	benchmark: string;
	capability: string;
	experiment_id: string;
	arms: Record<string, ReportMetrics>;
	verdict?: ReportVerdict;
}

export interface ReportReliability {
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

export interface ReportCardResponse {
	evaluation: string;
	arms: ReportArm[];
	capabilities: ReportCapability[];
	benchmarks: ReportBenchmark[];
	reliability?: ReportReliability[];
	safety?: ReportSafety;
	overall?: ReportOverall;
}

export interface AgentReportCardParams {
	agent: string;
	version?: number;
	model?: string;
}

export interface AgentLeaderboardRow {
	agent: string;
	version: number;
	model: string;
	label: string;
	baseline: boolean;
	capabilities: Record<string, { score: number; ci_low: number; ci_high: number; runs: number }>;
	cost_usd: number;
	last_evaluation: string;
}

/** The agent report card request: version and model only when set, so the server picks the latest. */
export function agentReportCardRequest({ agent, version, model }: AgentReportCardParams): { url: string; params?: Record<string, string> } {
	const params: Record<string, string> = {};
	if (version) params.version = String(version);
	if (model) params.model = model;
	const url = `/agents/${enc(agent)}/report-card`;
	return Object.keys(params).length > 0 ? { url, params } : { url };
}

export const evaluationsApi = benchApi
	.enhanceEndpoints({ addTagTypes: ["Evaluations", "AgentVersions", "AgentCompatibility", "AgentReportCard"] })
	.injectEndpoints({
		endpoints: (builder) => ({
			listEvaluations: builder.query<Evaluation[], void>({
				query: () => "/evaluations",
				providesTags: [{ type: "Evaluations", id: "LIST" }],
			}),
			getEvaluation: builder.query<Evaluation, string>({
				query: (id) => `/evaluations/${enc(id)}`,
				providesTags: (_r, _e, id) => [{ type: "Evaluations", id }],
			}),
			createEvaluation: builder.mutation<Evaluation, EvaluationSpec>({
				query: (body) => ({ url: "/evaluations", method: "POST", body }),
				invalidatesTags: [{ type: "Evaluations", id: "LIST" }],
			}),
			cancelEvaluation: builder.mutation<Evaluation, string>({
				query: (id) => ({ url: `/evaluations/${enc(id)}/cancel`, method: "POST" }),
				invalidatesTags: (_r, _e, id) => [{ type: "Evaluations", id }, { type: "Evaluations", id: "LIST" }],
			}),
			estimateEvaluation: builder.mutation<EvaluationEstimate, EvaluationSpec>({
				query: (body) => ({ url: "/evaluations/estimate", method: "POST", body }),
			}),
			getEvaluationPacks: builder.query<EvaluationPack[], void>({
				query: () => "/evaluation-packs",
			}),
			getEvaluationReport: builder.query<ReportCardResponse, string>({
				query: (id) => `/evaluations/${enc(id)}/report`,
				providesTags: (_r, _e, id) => [{ type: "Evaluations", id }],
			}),
			getAgentReportCard: builder.query<ReportCardResponse, AgentReportCardParams>({
				query: (params) => agentReportCardRequest(params),
				providesTags: (_r, _e, { agent }) => [{ type: "AgentReportCard", id: agent }],
			}),
			getAgentCompatibility: builder.query<AgentCompatibility, string>({
				query: (id) => `/agents/${enc(id)}/compatibility`,
				providesTags: (_r, _e, id) => [{ type: "AgentCompatibility", id }],
			}),
			getAgentVersions: builder.query<AgentVersionsResponse, string>({
				query: (id) => `/agents/${enc(id)}/versions`,
				providesTags: (_r, _e, id) => [{ type: "AgentVersions", id }],
			}),
			getAgentVersion: builder.query<AgentVersionDetail, { agent: string; version: number }>({
				query: ({ agent, version }) => `/agents/${enc(agent)}/versions/${version}`,
				providesTags: (_r, _e, { agent, version }) => [{ type: "AgentVersions", id: `${agent}@${version}` }],
			}),
			createAgentVersion: builder.mutation<AgentVersion, { agent: string; note: string }>({
				query: ({ agent, note }) => ({ url: `/agents/${enc(agent)}/versions`, method: "POST", body: { note } }),
				invalidatesTags: (_r, _e, { agent }) => [{ type: "AgentVersions", id: agent }],
			}),
			getAgentLeaderboard: builder.query<AgentLeaderboardRow[], { capability?: string; model?: string }>({
				query: ({ capability, model }) => {
					const params: Record<string, string> = {};
					if (capability) params.capability = capability;
					if (model) params.model = model;
					return Object.keys(params).length > 0 ? { url: "/leaderboard/agents", params } : "/leaderboard/agents";
				},
			}),
		}),
	});

export const {
	useListEvaluationsQuery,
	useGetEvaluationQuery,
	useCreateEvaluationMutation,
	useCancelEvaluationMutation,
	useEstimateEvaluationMutation,
	useGetEvaluationPacksQuery,
	useGetEvaluationReportQuery,
	useGetAgentReportCardQuery,
	useGetAgentCompatibilityQuery,
	useGetAgentVersionsQuery,
	useGetAgentVersionQuery,
	useCreateAgentVersionMutation,
	useGetAgentLeaderboardQuery,
} = evaluationsApi;
