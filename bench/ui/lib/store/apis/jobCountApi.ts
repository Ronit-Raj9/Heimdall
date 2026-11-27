import { benchApi } from "./benchApi";

/** What the UI keeps from GET /jobs?fields=count: null when the server does not know the count route. */
export interface JobCount {
	count: number | null;
	more: boolean;
}

/**
 * Reads the count answer. An older server ignores `fields=count` and sends the job list, so a
 * missing or non-number `count` gives null and the caller falls back to polling the list. Pure.
 */
export function jobCountFrom(raw: unknown): JobCount {
	const r = (raw ?? {}) as { count?: unknown; more?: unknown };
	return { count: typeof r.count === "number" ? r.count : null, more: r.more === true };
}

/** Number of running jobs, cheap to poll while the jobs drawer is closed and idle (X-108, M8-94). */
export const jobCountApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		countRunningJobs: builder.query<JobCount, void>({
			query: () => "/jobs?fields=count&status=running",
			transformResponse: (raw: unknown) => jobCountFrom(raw),
		}),
	}),
});

export const { useCountRunningJobsQuery } = jobCountApi;
