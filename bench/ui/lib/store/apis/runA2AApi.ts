import type { RunA2AResponse } from "@/lib/types/runA2A";
import { benchApi } from "./benchApi";

/** GET /runs/{id}/a2a: the agent gateway witness of an a2a run; available false for any other run. */
export const runA2AApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunA2A: builder.query<RunA2AResponse, string>({
			query: (runId) => runA2APath(runId),
		}),
	}),
});

/** The request path of one run's a2a witness, the run id escaped as one path segment. */
export function runA2APath(runId: string): string {
	return `/runs/${encodeURIComponent(runId)}/a2a`;
}

export const { useGetRunA2AQuery } = runA2AApi;