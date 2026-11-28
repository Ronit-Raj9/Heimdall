import type { JobResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/** POST /jobs/{id}/priority (handlers/jobpriority.go JobPriority): admin only, queued jobs only, -10 to 10. */
export function jobPriorityRequest(jobId: string, priority: number) {
	return { url: `/jobs/${encodeURIComponent(jobId)}/priority`, method: "POST", body: { priority } } as const;
}

/**
 * Round 5 lane L8: reorder a queued job. Attached to benchApi with the Jobs tag (the same tag platformApi's job
 * queries provide), not to platformApi, which many tests replace with a mock that has no api object.
 */
export const jobPriorityApi = benchApi.enhanceEndpoints({ addTagTypes: ["Jobs"] }).injectEndpoints({
	endpoints: (builder) => ({
		setJobPriority: builder.mutation<JobResponse, { id: string; priority: number }>({
			query: ({ id, priority }) => jobPriorityRequest(id, priority),
			invalidatesTags: (_r, _e, { id }) => [
				{ type: "Jobs", id },
				{ type: "Jobs", id: "LIST" },
			],
		}),
	}),
});

export const { useSetJobPriorityMutation } = jobPriorityApi;