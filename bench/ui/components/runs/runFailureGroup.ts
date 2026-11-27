import { benchApi } from "@/lib/store/apis/benchApi";

/**
 * The failure group of a run that did not pass (G1, M5-1): `GET /runs/{id}?failure=1` adds it when the
 * server can group the run's evaluation. Every field is optional: an older server sends nothing.
 */
export interface RunFailureGroup {
	signature?: string;
	title?: string;
	text?: string;
	/** Runs of the same evaluation with the same signature, this run included. */
	count?: number;
	exp_id?: string;
}

export const runFailureApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunFailure: builder.query<{ failure?: RunFailureGroup }, string>({
			query: (runId) => `/runs/${encodeURIComponent(runId)}?failure=1`,
			// Only the failure object is kept; the rest of the answer is the run detail the page already has.
			transformResponse: (raw: { failure?: RunFailureGroup } | undefined) => ({ failure: raw?.failure }),
			providesTags: (_result, _error, runId) => [{ type: "BenchRun", id: runId }],
		}),
	}),
});

export const { useGetRunFailureQuery } = runFailureApi;

/**
 * The "same failure in N runs" link, or undefined when the group is missing, has no signature, or holds
 * only this run. It opens the failure groups of the evaluation with that group selected.
 */
export function sameFailureLink(
	failure: RunFailureGroup | undefined,
	fallbackExpId?: string,
): { count: number; search: { tab: "clusters"; sig: string; exp?: string } } | undefined {
	const count = failure?.count ?? 0;
	if (!failure?.signature || !Number.isFinite(count) || count < 2) return undefined;
	const exp = failure.exp_id || fallbackExpId;
	return { count, search: { tab: "clusters", sig: failure.signature, ...(exp ? { exp } : {}) } };
}
