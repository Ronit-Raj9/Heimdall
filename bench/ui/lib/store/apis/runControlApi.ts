import { benchApi } from "./benchApi";
import type { Evaluation } from "./evaluationsApi";

/**
 * Run control (handlers/runcontrol.go, handlers/evaluationcontrol.go): pause and resume an
 * experiment or evaluation (in-flight runs finish, nothing new starts), live settings with a
 * hash-chained change log, cancel or requeue one run, and "Rerun failures" as an exploratory child.
 * The types mirror the Go JSON.
 */

export interface SlotHold {
	run_id?: string;
	by: string;
	reason?: string;
	at: string;
}

export interface SupersededRun {
	slot_id: string;
	outcome: string;
	by: string;
	reason?: string;
	at: string;
}

export interface RunControlState {
	mode: "running" | "paused";
	paused_by?: string;
	pause_reason?: string;
	paused_at?: string;
	concurrency?: number;
	total_usd?: number;
	paused_models?: string[];
	cancelled_slots?: Record<string, SlotHold>;
	superseded?: Record<string, SupersededRun>;
	seq: number;
	head?: string;
	updated_at?: string;
}

export interface ErrorPolicyRow {
	class: string;
	retry: "auto" | "on_resume" | "manual" | "never";
	max_attempts: number;
	uses_attempt: boolean;
	grade: "not_graded" | "unknown" | "zero" | "zero_veto";
	counts: boolean;
	editable: boolean;
	note: string;
}

export interface ControlLogEntry {
	seq: number;
	at: string;
	actor: string;
	action: "pause" | "resume" | "set" | "cancel_run" | "requeue_run" | "supersede" | string;
	target?: string;
	reason?: string;
	before: unknown;
	after: unknown;
	prev: string;
	hash: string;
}

/** GET /experiments/{id}/controls */
export interface RunControls {
	exp_id: string;
	state: RunControlState;
	paused: boolean;
	in_flight: number;
	models: string[];
	concurrency: number;
	total_usd: number;
	policy: ErrorPolicyRow[];
	log: ControlLogEntry[];
	log_entries: number;
	log_verified: boolean;
	log_problem?: string;
	rerun_of?: string;
	exploratory: boolean;
}

/** PATCH /experiments/{id}/controls body. Leave a field out to keep it; 0 clears an override. */
export interface RunControlsPatch {
	concurrency?: number;
	total_usd?: number;
	paused_models?: string[];
	reason?: string;
}

export interface RerunFailuresResult {
	parent_id: string;
	experiment_id: string;
	tasks: number;
	slots: number;
	config_changed?: string[];
	exploratory: boolean;
	started: boolean;
	start_error?: string;
}

export interface EvaluationRerunResult {
	evaluation: Evaluation;
	children: RerunFailuresResult[];
	skipped?: Record<string, string>;
}

const enc = encodeURIComponent;

export const runControlApi = benchApi.enhanceEndpoints({ addTagTypes: ["RunControls", "Evaluations"] }).injectEndpoints({
	endpoints: (builder) => ({
		getRunControls: builder.query<RunControls, string>({
			query: (expId) => `/experiments/${enc(expId)}/controls`,
			providesTags: (_r, _e, expId) => [{ type: "RunControls", id: expId }],
		}),
		pauseExperiment: builder.mutation<RunControls, { expId: string; reason?: string }>({
			query: ({ expId, reason }) => ({ url: `/experiments/${enc(expId)}/pause`, method: "POST", body: reason ? { reason } : {} }),
			invalidatesTags: (_r, _e, { expId }) => [
				{ type: "RunControls", id: expId },
				{ type: "BenchExperiment", id: expId },
			],
		}),
		unpauseExperiment: builder.mutation<unknown, { expId: string; reason?: string }>({
			query: ({ expId, reason }) => ({ url: `/experiments/${enc(expId)}/resume`, method: "POST", body: reason ? { reason } : {} }),
			invalidatesTags: (_r, _e, { expId }) => [
				{ type: "RunControls", id: expId },
				{ type: "BenchExperiment", id: expId },
				{ type: "BenchExperiment", id: "LIST" },
			],
		}),
		patchRunControls: builder.mutation<RunControls, { expId: string; patch: RunControlsPatch }>({
			query: ({ expId, patch }) => ({ url: `/experiments/${enc(expId)}/controls`, method: "PATCH", body: patch }),
			invalidatesTags: (_r, _e, { expId }) => [{ type: "RunControls", id: expId }],
		}),
		cancelRun: builder.mutation<RunControls, { expId: string; runId: string; reason?: string }>({
			query: ({ expId, runId, reason }) => ({
				url: `/experiments/${enc(expId)}/runs/${enc(runId)}/cancel`,
				method: "POST",
				body: reason ? { reason } : {},
			}),
			invalidatesTags: (_r, _e, { expId, runId }) => [
				{ type: "RunControls", id: expId },
				{ type: "BenchRun", id: runId },
				{ type: "BenchGrid", id: expId },
			],
		}),
		requeueRun: builder.mutation<RunControls, { expId: string; runId: string; reason?: string }>({
			query: ({ expId, runId, reason }) => ({
				url: `/experiments/${enc(expId)}/runs/${enc(runId)}/requeue`,
				method: "POST",
				body: reason ? { reason } : {},
			}),
			invalidatesTags: (_r, _e, { expId, runId }) => [
				{ type: "RunControls", id: expId },
				{ type: "BenchRun", id: runId },
				{ type: "BenchGrid", id: expId },
				{ type: "BenchExperiment", id: expId },
			],
		}),
		rerunFailures: builder.mutation<RerunFailuresResult, { expId: string; outcomes?: string[]; reason?: string }>({
			query: ({ expId, outcomes, reason }) => ({
				url: `/experiments/${enc(expId)}/rerun-failures`,
				method: "POST",
				body: { ...(outcomes?.length ? { outcomes } : {}), ...(reason ? { reason } : {}) },
			}),
			invalidatesTags: [{ type: "BenchExperiment", id: "LIST" }],
		}),
		pauseEvaluation: builder.mutation<Evaluation, { evalId: string; reason?: string }>({
			query: ({ evalId, reason }) => ({ url: `/evaluations/${enc(evalId)}/pause`, method: "POST", body: reason ? { reason } : {} }),
			invalidatesTags: (_r, _e, { evalId }) => [{ type: "Evaluations", id: evalId }, { type: "Evaluations", id: "LIST" }, "RunControls"],
		}),
		resumeEvaluation: builder.mutation<Evaluation, { evalId: string; reason?: string }>({
			query: ({ evalId, reason }) => ({ url: `/evaluations/${enc(evalId)}/resume`, method: "POST", body: reason ? { reason } : {} }),
			invalidatesTags: (_r, _e, { evalId }) => [{ type: "Evaluations", id: evalId }, { type: "Evaluations", id: "LIST" }, "RunControls"],
		}),
		rerunEvaluationFailures: builder.mutation<EvaluationRerunResult, { evalId: string; reason?: string }>({
			query: ({ evalId, reason }) => ({
				url: `/evaluations/${enc(evalId)}/rerun-failures`,
				method: "POST",
				body: reason ? { reason } : {},
			}),
			invalidatesTags: [{ type: "Evaluations", id: "LIST" }],
		}),
	}),
});

export const {
	useGetRunControlsQuery,
	usePauseExperimentMutation,
	useUnpauseExperimentMutation,
	usePatchRunControlsMutation,
	useCancelRunMutation,
	useRequeueRunMutation,
	useRerunFailuresMutation,
	usePauseEvaluationMutation,
	useResumeEvaluationMutation,
	useRerunEvaluationFailuresMutation,
} = runControlApi;

/** Whether a run outcome may be requeued: only aborted_infra and infra_error, never a graded run. */
export function requeueable(outcome: string | undefined): boolean {
	return outcome === "aborted_infra" || outcome === "infra_error";
}

/** The live concurrency in effect: the override, else the experiment's own. */
export function effectiveConcurrency(c: RunControls): number {
	return c.state.concurrency && c.state.concurrency > 0 ? c.state.concurrency : c.concurrency;
}

/** The spend cap in effect: the live override, else the frozen one (0 = none). */
export function effectiveCap(c: RunControls): number {
	return c.state.total_usd && c.state.total_usd > 0 ? c.state.total_usd : c.total_usd;
}