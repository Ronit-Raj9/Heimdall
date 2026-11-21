import { benchApi } from "./benchApi";

/**
 * Evaluations: one agent (or two arms for A/B) run across many benchmarks at once, plus the report
 * card built from the child experiments. Types follow audit/AGENTFIRST_CONTRACT.md sections 3-4.
 */

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

/** Per-arm metrics for one capability or one benchmark. */
export interface ReportArmMetrics {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
	cost_usd: number;
	tokens: number;
	steps: number;
	tool_calls: number;
	wall_s: number;
}

export interface ReportCapability {
	capability: string;
	benchmarks: string[];
	arms: Record<string, ReportArmMetrics>;
}

export interface ReportVerdict {
	winner: string; // arm label, or "tie"
	delta: number;
	significant: boolean;
	summary: string;
}

export interface ReportBenchmark {
	benchmark: string;
	capability: string;
	experiment_id: string;
	arms: Record<string, ReportArmMetrics>;
	verdict?: ReportVerdict;
}

export interface ReportReliability {
	fault: string;
	arms: Record<string, { score: number; drop: number }>;
}

export interface ReportSafety {
	attack_resisted: Record<string, number>;
	cheat_rate: Record<string, number>;
}

export interface ReportOverall {
	winner: string;
	wins: Record<string, number>;
	summary: string;
}

export interface Report {
	evaluation: string;
	arms: EvaluationArm[];
	capabilities: ReportCapability[];
	benchmarks: ReportBenchmark[];
	reliability: ReportReliability[];
	safety?: ReportSafety;
	overall?: ReportOverall;
}

export interface AgentReportCardParams {
	agent: string;
	version?: number;
	model?: string;
}

export const evaluationsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		listEvaluations: builder.query<Evaluation[], void>({
			query: () => "/evaluations",
		}),
		getEvaluation: builder.query<Evaluation, string>({
			query: (id) => `/evaluations/${encodeURIComponent(id)}`,
		}),
		createEvaluation: builder.mutation<Evaluation, EvaluationSpec>({
			query: (body) => ({ url: "/evaluations", method: "POST", body }),
		}),
		cancelEvaluation: builder.mutation<Evaluation, string>({
			query: (id) => ({ url: `/evaluations/${encodeURIComponent(id)}/cancel`, method: "POST" }),
		}),
		estimateEvaluation: builder.mutation<EvaluationEstimate, EvaluationSpec>({
			query: (body) => ({ url: "/evaluations/estimate", method: "POST", body }),
		}),
		getEvaluationPacks: builder.query<EvaluationPack[], void>({
			query: () => "/evaluation-packs",
		}),
		getEvaluationReport: builder.query<Report, string>({
			query: (id) => `/evaluations/${encodeURIComponent(id)}/report`,
		}),
		getAgentReportCard: builder.query<Report, AgentReportCardParams>({
			query: ({ agent, version, model }) => {
				const params: Record<string, string> = {};
				if (version) params.version = String(version);
				if (model) params.model = model;
				return { url: `/agents/${encodeURIComponent(agent)}/report-card`, params };
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
} = evaluationsApi;