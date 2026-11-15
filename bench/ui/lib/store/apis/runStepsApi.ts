import type { RunStepsResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * GET /runs/{id}/steps: the run's unified agent step timeline and its observer status, injected
 * into the bench API so the shared endpoint list stays untouched. A run recorded before the
 * timeline answers available false with empty lists, not an error.
 */
export const runStepsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunSteps: builder.query<RunStepsResponse, string>({
			query: (runId) => runStepsPath(runId),
		}),
	}),
});

/** The request path of one run's step timeline, with the run id escaped as one path segment. */
export function runStepsPath(runId: string): string {
	return `/runs/${encodeURIComponent(runId)}/steps`;
}

export const { useGetRunStepsQuery } = runStepsApi;