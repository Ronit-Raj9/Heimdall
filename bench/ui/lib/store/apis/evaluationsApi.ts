import { benchApi } from "./benchApi";

/**
 * Evaluations (agent-first contract section 3): one agent (or two arms for an A/B) run on many
 * benchmarks at once, one normal experiment per benchmark. Types mirror schemas/evaluation.go.
 * Injected into the bench API so the shared endpoint list stays untouched.
 */

export type EvaluationMode = "score" | "versions" | "models" | "opponent";
export type EvaluationStatus = "queued" | "running" | "done" | "failed" | "cancelled";

export interface EvaluationArm {
	label: string;
	agent: string;
	version?: number;
	model: string;
	provider?: string;
}

export interface EvaluationSpec {
	name: string;
	/** Typed as text too, so a newer server's mode still renders. */
	mode: EvaluationMode | (string & {});
	/** 1 arm for score, 2 for A/B. */
	arms: EvaluationArm[];
	/** Suite ids. */
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
	/** Why the benchmark was not run (agent lacks a capability, no ready tasks). */
	skipped?: string;
}

export interface Evaluation {
	id: string;
	org?: string;
	spec: EvaluationSpec;
	status: EvaluationStatus | (string & {});
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
	known?: boolean;
}

export interface EvaluationEstimate {
	runs: number;
	est_cost_usd: EvaluationRange & { known: boolean };
	est_minutes: EvaluationRange;
	per_benchmark: unknown[];
}

/** One arm's measurements on a capability or benchmark (contract section 4). */
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

export interface EvaluationReport {
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
	safety?: { attack_resisted?: Record<string, number>; cheat_rate?: Record<string, number> };
	overall?: { winner: string; wins: Record<string, number>; summary: string };
}

/** The list answers either a bare array or {evaluations: [...]}; both become an array. */
export function normalizeEvaluationList(raw: unknown): Evaluation[] {
	if (Array.isArray(raw)) return raw as Evaluation[];
	if (raw && typeof raw === "object" && Array.isArray((raw as { evaluations?: unknown }).evaluations)) {
		return (raw as { evaluations: Evaluation[] }).evaluations;
	}
	return [];
}

function evalPath(id: string): string {
	return `/evaluations/${encodeURIComponent(id)}`;
}

export const evaluationsApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchEvaluation"] }).injectEndpoints({
	endpoints: (builder) => ({
		listEvaluations: builder.query<Evaluation[], void>({
			query: () => "/evaluations",
			transformResponse: normalizeEvaluationList,
			providesTags: [{ type: "BenchEvaluation", id: "LIST" }],
		}),
		getEvaluation: builder.query<Evaluation, string>({
			query: (id) => evalPath(id),
			providesTags: (_r, _e, id) => [{ type: "BenchEvaluation", id }],
		}),
		createEvaluation: builder.mutation<Evaluation, EvaluationSpec>({
			query: (body) => ({ url: "/evaluations", method: "POST", body }),
			invalidatesTags: [{ type: "BenchEvaluation", id: "LIST" }],
		}),
		cancelEvaluation: builder.mutation<Evaluation, string>({
			query: (id) => ({ url: `${evalPath(id)}/cancel`, method: "POST" }),
			invalidatesTags: (_r, _e, id) => [
				{ type: "BenchEvaluation", id },
				{ type: "BenchEvaluation", id: "LIST" },
			],
		}),
		estimateEvaluation: builder.mutation<EvaluationEstimate, EvaluationSpec>({
			query: (body) => ({ url: "/evaluations/estimate", method: "POST", body }),
		}),
		getEvaluationPacks: builder.query<EvaluationPack[], void>({
			query: () => "/evaluation-packs",
		}),
		getEvaluationReport: builder.query<EvaluationReport, string>({
			query: (id) => `${evalPath(id)}/report`,
			providesTags: (_r, _e, id) => [{ type: "BenchEvaluation", id: `report:${id}` }],
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
} = evaluationsApi;