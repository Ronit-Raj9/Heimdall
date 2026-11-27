import { useCountRunningJobsQuery } from "@/lib/store/apis/jobCountApi";
import { useListJobsQuery } from "@/lib/store/apis/platformApi";
import type { Job } from "@/lib/types/bench";
import { useEffect, useMemo, useRef } from "react";

const AGENT_JOBS_POLL_MS = 15000;
const poll = { pollingInterval: AGENT_JOBS_POLL_MS, skipPollingIfUnfocused: true };

/**
 * X-78: the cheap running count is polled; the full job list polls only while a job runs
 * (or while the server cannot count), and is read once otherwise. Pure.
 */
export function agentJobsPollMs(running: number | null | undefined): number {
	return running === 0 ? 0 : AGENT_JOBS_POLL_MS;
}

/** GET /jobs, shared by every agent card; undefined while loading or when the jobs API is unavailable. */
export function useAgentJobs(): readonly Job[] | undefined {
	const count = useCountRunningJobsQuery(undefined, poll);
	const running = count.data?.count;
	const { data, refetch } = useListJobsQuery(undefined, { pollingInterval: agentJobsPollMs(running), skipPollingIfUnfocused: true });
	// A change in the running count (a job started or ended) reads the list once more, so check states are not left behind.
	const last = useRef(running);
	useEffect(() => {
		const before = last.current;
		last.current = running;
		if (typeof before === "number" && typeof running === "number" && before !== running) void refetch();
	}, [running, refetch]);
	return useMemo(() => data?.jobs, [data]);
}

/** True when the jobs call failed, so the list can say its check states may be old (M2-25). Same cached query as useAgentJobs. */
export function useAgentJobsError(): boolean {
	const { isError } = useListJobsQuery(undefined, { pollingInterval: 0 });
	return isError;
}
