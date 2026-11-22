import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/**
 * Recordings, live screen and takeover of browser and desktop runs (lane Z03):
 * handlers/runrecordings.go and handlers/runlive.go. The types mirror the Go JSON.
 */

/** One recorded file (GET /runs/{id}/recordings). */
export interface RecordingView {
	index: number;
	service: string;
	id: string;
	kind: "video" | "har" | "console" | "log";
	file?: string;
	mime?: string;
	bytes?: number;
	sha256?: string;
	started_at?: string;
	error?: string;
	url?: string;
}

/** One tool call of a recorded service, for syncing the video with the trace. */
export interface RecordingStep {
	seq: number;
	service: string;
	tool: string;
	label: string;
	started: string;
	latency_ms: number;
	is_error?: boolean;
}

/** One input a person sent in a debug run. */
export interface HumanAction {
	seq: number;
	at: string;
	service: string;
	action: "click" | "type" | "key" | "scroll";
	x?: number;
	y?: number;
	text?: string;
	combo?: string;
	direction?: string;
	by?: string;
	/** How by reads (a member's display name). */
	by_name?: string;
	error?: string;
}

export interface RunRecordings {
	run_id: string;
	sealed: boolean;
	recordings: RecordingView[];
	steps: RecordingStep[];
	human_assisted?: boolean;
	actions?: HumanAction[];
}

/** GET /runs/{id}/live: whether a running run shows its screen. */
export interface RunLiveStatus {
	run_id: string;
	live: boolean;
	service?: string;
	mime?: string;
	at?: string;
	debug?: boolean;
	takeover_ok?: boolean;
	pending?: number;
	frame_url?: string;
}

/** POST /runs/{id}/takeover body. */
export interface TakeoverInput {
	action: "click" | "type" | "key" | "scroll";
	x?: number;
	y?: number;
	text?: string;
	combo?: string;
	direction?: "up" | "down" | "left" | "right";
	frame_w?: number;
	frame_h?: number;
}

const enc = encodeURIComponent;

export const recordingsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunRecordings: builder.query<RunRecordings, string>({
			query: (runId) => `/runs/${enc(runId)}/recordings`,
		}),
		getRunLive: builder.query<RunLiveStatus, string>({
			query: (runId) => `/runs/${enc(runId)}/live`,
		}),
		takeoverRun: builder.mutation<HumanAction, { runId: string; input: TakeoverInput }>({
			query: ({ runId, input }) => ({ url: `/runs/${enc(runId)}/takeover`, method: "POST", body: input }),
		}),
	}),
});

/** The URL of recording n of a run (served in byte ranges, so a video can seek). */
export function recordingUrl(runId: string, n: number): string {
	return `${BENCH_API_BASE}/runs/${enc(runId)}/recordings/${n}`;
}

/** The newest live frame of a run; at busts the browser cache per frame. */
export function liveFrameUrl(runId: string, at?: string): string {
	return `${BENCH_API_BASE}/runs/${enc(runId)}/live/frame${at ? `?at=${enc(at)}` : ""}`;
}

export const { useGetRunRecordingsQuery, useGetRunLiveQuery, useTakeoverRunMutation } = recordingsApi;