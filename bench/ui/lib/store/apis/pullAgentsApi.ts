import { benchApi } from "./benchApi";

/** One pull-mode agent the org's processes serve (GET /pull/agents). */
export interface PullAgentStatus {
	agent: string;
	connected: boolean;
	last_poll: string;
	processes: number;
	queued: number;
	in_flight: number;
}

export interface PullAgentsResponse {
	agents: PullAgentStatus[] | null;
}

/** Pull-mode agents (lane Y11): which agents a process in the customer's code serves right now. */
export const pullAgentsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getPullAgents: builder.query<PullAgentsResponse, void>({
			query: () => "/pull/agents",
		}),
	}),
});

export const { useGetPullAgentsQuery } = pullAgentsApi;
