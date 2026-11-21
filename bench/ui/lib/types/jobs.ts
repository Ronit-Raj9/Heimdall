import type { JobStatus } from "@/lib/types/bench";

/** Query of GET /jobs (handlers/jobs.go list): every field optional. */
export interface JobListFilter {
	kind?: string;
	status?: JobStatus;
	target?: string;
	/** The next cursor of the previous page. */
	before?: string;
	limit?: number;
}

/** Job statuses the jobs page filters on, in lifecycle order. */
export const JOB_STATUSES: readonly JobStatus[] = ["queued", "running", "succeeded", "failed", "cancelled"];

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		jobs: typeof import("@/locales/en/jobs.json");
	}
}
