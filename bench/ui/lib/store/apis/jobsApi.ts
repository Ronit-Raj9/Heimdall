import type { JobListResponse } from "@/lib/types/bench";
import type { JobListFilter } from "@/lib/types/jobs";
import { benchApi } from "./benchApi";

/** GET /jobs with filters (lane L02, app/jobs). Shares the "Jobs" LIST tag so cancel and create refresh it. */
export const jobsApi = benchApi.enhanceEndpoints({ addTagTypes: ["Jobs"] }).injectEndpoints({
	endpoints: (builder) => ({
		listJobsFiltered: builder.query<JobListResponse, JobListFilter>({
			query: (filter) => {
				const params: Record<string, string> = {};
				for (const [key, value] of Object.entries(filter)) {
					if (value !== undefined && value !== "") params[key] = String(value);
				}
				return { url: "/jobs", params };
			},
			providesTags: [{ type: "Jobs", id: "LIST" }],
		}),
	}),
});

export const { useListJobsFilteredQuery } = jobsApi;
