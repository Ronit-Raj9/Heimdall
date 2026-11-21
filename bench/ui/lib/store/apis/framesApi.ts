import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

/** One screen replay frame (GET /runs/{id}/frames, handlers/runframes.go). */
export interface ReplayFrame {
	index: number;
	seq?: number;
	timestamp?: string;
	offset_ms: number;
	tool?: string;
	action?: string;
	image: string;
	url: string;
}

export interface RunFrames {
	run_id: string;
	live: boolean;
	frames: ReplayFrame[];
}

/** Screen replay frames of computer-use and browser runs, injected into the bench API. */
export const framesApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunFrames: builder.query<RunFrames, string>({
			query: (runId) => `/runs/${encodeURIComponent(runId)}/frames`,
		}),
	}),
});

/** The image URL of frame n of a run. */
export function frameImageUrl(runId: string, n: number): string {
	return `${BENCH_API_BASE}/runs/${encodeURIComponent(runId)}/frames/${n}`;
}

export const { useGetRunFramesQuery } = framesApi;
