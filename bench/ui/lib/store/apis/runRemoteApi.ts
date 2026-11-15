import type { RunRemoteResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * GET /runs/{id}/remote: the channel summary of a remote agent's run (docs/remote-execution.md),
 * injected into the bench API so the shared endpoint list stays untouched. A run of any other
 * agent answers available false, not an error.
 */
export const runRemoteApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunRemote: builder.query<RunRemoteResponse, string>({
			query: (runId) => runRemotePath(runId),
		}),
	}),
});

/** The request path of one run's remote coverage summary, with the run id escaped as one path segment. */
export function runRemotePath(runId: string): string {
	return `/runs/${encodeURIComponent(runId)}/remote`;
}

export const { useGetRunRemoteQuery } = runRemoteApi;
