import { benchApi } from "./benchApi";

// Agent-first evaluations (audit/AGENTFIRST_CONTRACT.md sections 1, 3 and 4). One evaluation runs one
// agent (or two arms for A/B) across many benchmarks and ends in a report card.

export type Capability = "shell" | "tools" | "mcp" | "sessions" | "browser" | "desktop" | "voice" | "gpu";

export interface EvaluationArm {
	label: string;
	agent: string;
	version?: number;
	model: string;
	provider?: string;
}

export type EvaluationMode = "score" | "versions" | "models" | "opponent";
export type EvaluationFault = "clean" | "model-down" | "broken-tool" | "misleading-instruction";

export interface EvaluationSpec {
	name: string;
	mode: EvaluationMode;
	/** 1 arm for score, 2 for A/B. */
	arms: EvaluationArm[];
	/** Benchmark (suite) ids; or pack. */
	benchmarks: string[];
	pack?: string;
	/** Default 5, admitted tasks only. */
	tasks_per_benchmark: number;
	/** Repeats per task, default 2. */
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

export type EvaluationStatus = "queued" | "running" | "done" | "failed" | "cancelled";

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

export interface EvaluationRange {
	low: number;
	high: number;
}

export interface EvaluationEstimate {
	runs: number;
	est_cost_usd: EvaluationRange & { known: boolean };
	est_minutes: EvaluationRange;
	per_benchmark: Record<string, unknown>[];
}

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

export interface ReportVerdict {
	winner: string;
	delta: number;
	significant: boolean;
	summary: string;
}

export interface ReportCard {
	evaluation: string;
	arms: EvaluationArm[];
	capabilities: { capability: string; benchmarks: string[]; arms: Record<string, ReportMetrics> }[];
	benchmarks: {
		benchmark: string;
		capability: string;
		experiment_id: string;
		arms: Record<string, ReportMetrics>;
		verdict?: ReportVerdict;
	}[];
	reliability: { fault: string; arms: Record<string, { score: number; drop: number }> }[];
	safety: { attack_resisted: Record<string, number>; cheat_rate: Record<string, number> };
	overall: { winner: string; wins: Record<string, number>; summary: string };
}

export interface AgentVersion {
	version: number;
	hash: string;
	created_at: string;
	created_by: string;
	note: string;
	runs?: number;
}

const enc = encodeURIComponent;

export const evaluationsApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchEvaluations", "BenchAgentVersions"] }).injectEndpoints({
	endpoints: (builder) => ({
		getEvaluations: builder.query<Evaluation[], void>({
			query: () => "/evaluations",
			providesTags: [{ type: "BenchEvaluations", id: "LIST" }],
		}),
		getEvaluation: builder.query<Evaluation, string>({
			query: (id) => `/evaluations/${enc(id)}`,
			providesTags: (_r, _e, id) => [{ type: "BenchEvaluations", id }],
		}),
		createEvaluation: builder.mutation<Evaluation, EvaluationSpec>({
			query: (body) => ({ url: "/evaluations", method: "POST", body }),
			invalidatesTags: [{ type: "BenchEvaluations", id: "LIST" }],
		}),
		cancelEvaluation: builder.mutation<Evaluation, string>({
			query: (id) => ({ url: `/evaluations/${enc(id)}/cancel`, method: "POST" }),
			invalidatesTags: (_r, _e, id) => [
				{ type: "BenchEvaluations", id },
				{ type: "BenchEvaluations", id: "LIST" },
			],
		}),
		estimateEvaluation: builder.mutation<EvaluationEstimate, EvaluationSpec>({
			query: (body) => ({ url: "/evaluations/estimate", method: "POST", body }),
		}),
		getEvaluationPacks: builder.query<EvaluationPack[], void>({
			query: () => "/evaluation-packs",
		}),
		getEvaluationReport: builder.query<ReportCard, string>({
			query: (id) => `/evaluations/${enc(id)}/report`,
			providesTags: (_r, _e, id) => [{ type: "BenchEvaluations", id: `${id}/report` }],
		}),
		getAgentCompatibility: builder.query<AgentCompatibility, string>({
			query: (agent) => `/agents/${enc(agent)}/compatibility`,
		}),
		getAgentReportCard: builder.query<ReportCard, { agent: string; version?: number; model?: string }>({
			query: ({ agent, version, model }) => {
				const params: Record<string, string> = {};
				if (version) params.version = String(version);
				if (model) params.model = model;
				return { url: `/agents/${enc(agent)}/report-card`, params };
			},
		}),
		getAgentVersions: builder.query<{ agent: string; versions: AgentVersion[] }, string>({
			query: (agent) => `/agents/${enc(agent)}/versions`,
			providesTags: (_r, _e, agent) => [{ type: "BenchAgentVersions", id: agent }],
		}),
		getAgentVersion: builder.query<{ version: number; hash: string; yaml: string }, { agent: string; version: number }>({
			query: ({ agent, version }) => `/agents/${enc(agent)}/versions/${version}`,
		}),
		createAgentVersion: builder.mutation<AgentVersion, { agent: string; note?: string }>({
			query: ({ agent, note }) => ({ url: `/agents/${enc(agent)}/versions`, method: "POST", body: { note: note ?? "" } }),
			invalidatesTags: (_r, _e, { agent }) => [{ type: "BenchAgentVersions", id: agent }],
		}),
	}),
});

export const {
	useGetEvaluationsQuery,
	useGetEvaluationQuery,
	useCreateEvaluationMutation,
	useCancelEvaluationMutation,
	useEstimateEvaluationMutation,
	useGetEvaluationPacksQuery,
	useGetEvaluationReportQuery,
	useGetAgentCompatibilityQuery,
	useGetAgentReportCardQuery,
	useGetAgentVersionsQuery,
	useGetAgentVersionQuery,
	useCreateAgentVersionMutation,
} = evaluationsApi;