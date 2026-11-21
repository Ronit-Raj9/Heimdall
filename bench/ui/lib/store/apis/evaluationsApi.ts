import { benchApi } from "./benchApi";

// Evaluations (agent-first contract section 3): one evaluation runs many benchmarks for one or two arms.

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
	/** 1 arm for score, 2 for A/B. */
	arms: EvaluationArm[];
	/** Benchmark (suite) ids; or a pack. */
	benchmarks: string[];
	pack?: string;
	/** Default 5, admitted tasks only. */
	tasks_per_benchmark: number;
	/** Repeats per task, default 2. */
	trials: number;
	/** "clean" always; plus "model-down", "broken-tool", "misleading-instruction". */
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

export interface EvaluationRange {
	low: number;
	high: number;
}

export interface EvaluationEstimate {
	runs: number;
	est_cost_usd: EvaluationRange & { known: boolean };
	est_minutes: EvaluationRange;
	per_benchmark?: unknown[];
}

export const evaluationsApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchEvaluation"] }).injectEndpoints({
	endpoints: (builder) => ({
		listEvaluations: builder.query<Evaluation[], void>({
			query: () => "/evaluations",
			providesTags: [{ type: "BenchEvaluation", id: "LIST" }],
		}),
		getEvaluation: builder.query<Evaluation, string>({
			query: (id) => `/evaluations/${encodeURIComponent(id)}`,
			providesTags: (_r, _e, id) => [{ type: "BenchEvaluation", id }],
		}),
		createEvaluation: builder.mutation<Evaluation, EvaluationSpec>({
			query: (body) => ({ url: "/evaluations", method: "POST", body }),
			invalidatesTags: [{ type: "BenchEvaluation", id: "LIST" }],
		}),
		cancelEvaluation: builder.mutation<Evaluation, string>({
			query: (id) => ({ url: `/evaluations/${encodeURIComponent(id)}/cancel`, method: "POST" }),
			invalidatesTags: (_r, _e, id) => [
				{ type: "BenchEvaluation", id },
				{ type: "BenchEvaluation", id: "LIST" },
			],
		}),
		/** A read with a body: cached per spec so the wizard's live estimate refetches only on change. */
		estimateEvaluation: builder.query<EvaluationEstimate, EvaluationSpec>({
			query: (body) => ({ url: "/evaluations/estimate", method: "POST", body }),
		}),
		listEvaluationPacks: builder.query<EvaluationPack[], void>({
			query: () => "/evaluation-packs",
		}),
	}),
});

export const {
	useListEvaluationsQuery,
	useGetEvaluationQuery,
	useCreateEvaluationMutation,
	useCancelEvaluationMutation,
	useEstimateEvaluationQuery,
	useListEvaluationPacksQuery,
} = evaluationsApi;