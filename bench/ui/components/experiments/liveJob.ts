import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { useEffect, useState } from "react";

/** Job states after which nothing changes. */
export const JOB_ENDED = new Set(["succeeded", "failed", "cancelled"]);

/** The polling interval for a job: every `ms` while it runs, never once it ended (M1-62). Pure. */
export function jobPollMs(status: string | undefined, ms: number): number {
	return status && JOB_ENDED.has(status) ? 0 : ms;
}

/**
 * One job, polled every `ms` while it runs and never again once it ended (M1-62), and only while the
 * tab has focus. A null id asks nothing.
 */
export function useLiveJob(jobId: string | null, ms: number) {
	const [ended, setEnded] = useState(false);
	const job = useGetJobQuery(jobId ?? skipToken, { pollingInterval: ended ? 0 : ms, skipPollingIfUnfocused: true });
	const status = job.data && job.data.id === jobId ? job.data.status : undefined;
	const now = jobPollMs(status, ms) === 0;
	useEffect(() => setEnded(now), [now]);
	return job;
}
