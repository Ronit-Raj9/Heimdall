import type { Decision, ReleaseRecord, ReleaseSummary } from "@/lib/types/bench";
import type { Schedule } from "@/lib/types/schedules";
import { benchApi } from "./benchApi";
import type { EvaluationGateResult, GateBaseline } from "./evaluationsApi";

/**
 * Releases from evaluations (lane X12): one release covers a whole evaluation and names the agent
 * version it puts live and the version it replaces. Types mirror bench/handlers/evalrelease.go.
 */

/** GET /agents/{id}/live-version. version 0: nothing released yet. */
export interface LiveVersion {
	agent: string;
	version: number;
	release_id?: string;
	released_at?: string;
	approver?: string;
	evaluation_id?: string;
}

export interface EvaluationReleaseChild {
	benchmark: string;
	experiment_id: string;
	status: string;
	/** Finished, so its runs are evidence of the release. */
	included: boolean;
}

/** GET /evaluations/{id}/release-preview */
export interface EvaluationReleasePreview {
	evaluation: string;
	status: string;
	mode: string;
	agent: string;
	version: number;
	replaces_version: number;
	live: LiveVersion;
	candidate: string;
	baseline?: GateBaseline;
	summary?: ReleaseSummary;
	gate?: EvaluationGateResult;
	decision: Decision | "";
	children: EvaluationReleaseChild[];
	/** Why the release needs an override. */
	blocked: string[];
	/** Can be released without an override. */
	ready: boolean;
	/** Why it cannot be released at all. */
	problem?: string;
}

/** POST /evaluations/{id}/release */
export interface EvaluationReleaseRequest {
	/** Default: the signed-in user. */
	approver?: string;
	note?: string;
	override?: string;
}

export type EvaluationCadence = "nightly" | "weekly";

/** POST /evaluations/{id}/schedule */
export interface EvaluationScheduleRequest {
	cadence: EvaluationCadence;
	max_usd_per_day?: number;
	timezone?: string;
}

export interface EvaluationScheduleResponse {
	schedule: Schedule;
	next?: string;
}

const enc = encodeURIComponent;

export const evalReleaseApi = benchApi
	.enhanceEndpoints({ addTagTypes: ["EvalRelease", "BenchSchedules", "Evaluations"] })
	.injectEndpoints({
		endpoints: (builder) => ({
			getEvaluationReleasePreview: builder.query<EvaluationReleasePreview, string>({
				query: (id) => `/evaluations/${enc(id)}/release-preview`,
				providesTags: (_r, _e, id) => [{ type: "EvalRelease", id }, { type: "BenchRelease", id: "LIST" }],
			}),
			releaseEvaluation: builder.mutation<ReleaseRecord, { id: string } & EvaluationReleaseRequest>({
				query: ({ id, ...body }) => ({ url: `/evaluations/${enc(id)}/release`, method: "POST", body }),
				invalidatesTags: (_r, _e, { id }) => [{ type: "EvalRelease", id }, { type: "BenchRelease", id: "LIST" }, { type: "EvalRelease", id: "LIVE" }],
			}),
			scheduleEvaluation: builder.mutation<EvaluationScheduleResponse, { id: string } & EvaluationScheduleRequest>({
				query: ({ id, ...body }) => ({ url: `/evaluations/${enc(id)}/schedule`, method: "POST", body }),
				invalidatesTags: ["BenchSchedules"],
			}),
			getLiveVersion: builder.query<LiveVersion, string>({
				query: (agent) => `/agents/${enc(agent)}/live-version`,
				providesTags: [{ type: "EvalRelease", id: "LIVE" }],
			}),
		}),
	});

export const { useGetEvaluationReleasePreviewQuery, useReleaseEvaluationMutation, useScheduleEvaluationMutation, useGetLiveVersionQuery } =
	evalReleaseApi;
