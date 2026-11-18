import type { CodeScanReportResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * X4: GET /runs/{id}/codescan, the security findings on the lines a run's agent added, injected into
 * the bench API so the shared endpoint list stays untouched. A run with no diff answers scanned false
 * with the reason, not an error.
 */
export const codeScanApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunCodeScan: builder.query<CodeScanReportResponse, string>({
			query: (runId) => runCodeScanPath(runId),
		}),
	}),
});

/** The request path of one run's code scan, with the run id escaped as one path segment. */
export function runCodeScanPath(runId: string): string {
	return `/runs/${encodeURIComponent(runId)}/codescan`;
}

export const { useGetRunCodeScanQuery } = codeScanApi;