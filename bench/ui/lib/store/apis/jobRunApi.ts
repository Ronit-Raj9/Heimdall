import type { JobResponse } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/** Body of POST /jobs: a job kind and its params (web parity, components/jobs/jobRunButton). */
export interface CreateJobRequest {
	kind: string;
	params?: object;
}

/** POST /jobs, the existing job API (handlers/jobs.go). */
export const jobRunApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		createJob: builder.mutation<JobResponse, CreateJobRequest>({
			query: (body) => ({ url: "/jobs", method: "POST", body: { kind: body.kind, params: body.params ?? {} } }),
		}),
	}),
});

export const { useCreateJobMutation } = jobRunApi;
