import { useListJobsQuery } from "@/lib/store/apis/platformApi";
import type { Job } from "@/lib/types/bench";
import { useMemo } from "react";

/** GET /jobs, shared by every agent card; undefined while loading or when the jobs API is unavailable. */
export function useAgentJobs(): readonly Job[] | undefined {
	const { data } = useListJobsQuery(undefined, { pollingInterval: 15000 });
	return useMemo(() => data?.jobs, [data]);
}

/** True when the jobs call failed, so the list can say its check states may be old (M2-25). Same cached query as useAgentJobs. */
export function useAgentJobsError(): boolean {
	const { isError } = useListJobsQuery(undefined, { pollingInterval: 15000 });
	return isError;
}
