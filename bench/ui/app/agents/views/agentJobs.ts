import { useListJobsQuery } from "@/lib/store/apis/platformApi";
import type { Job } from "@/lib/types/bench";
import { useMemo } from "react";

/** GET /jobs, shared by every agent card; undefined while loading or when the jobs API is unavailable. */
export function useAgentJobs(): readonly Job[] | undefined {
	const { data } = useListJobsQuery(undefined, { pollingInterval: 15000 });
	return useMemo(() => data?.jobs, [data]);
}
