import { benchApi } from "./benchApi";

/** One capability score of an agent leaderboard row: solved share of runs with a 95% range. */
export interface AgentCapabilityScore {
	score: number;
	ci_low: number;
	ci_high: number;
	runs: number;
	benchmarks?: string[];
}

/** One agent version on one model (GET /leaderboard/agents). */
export interface AgentLeaderboardRow {
	agent: string;
	/** 0 when the config is not a saved version. */
	version: number;
	model: string;
	label: string;
	config_hash?: string;
	baseline: boolean;
	capabilities: Record<string, AgentCapabilityScore>;
	cost_usd: number | null;
	runs?: number;
	last_evaluation: string;
	last_experiment?: string;
}

export interface AgentLeaderboardResponse {
	capabilities: string[];
	models: string[];
	capability: string;
	model: string;
	rows: AgentLeaderboardRow[];
	generated_at: string;
}

export interface AgentLeaderboardParams {
	capability?: string;
	model?: string;
}

/** The request for the agent leaderboard: filters with a value, or the bare route. */
export function agentLeaderboardRequest(params: AgentLeaderboardParams): string | { url: string; params: Record<string, string> } {
	const search = Object.fromEntries(
		Object.entries(params).filter((entry): entry is [string, string] => typeof entry[1] === "string" && entry[1] !== ""),
	);
	return Object.keys(search).length > 0 ? { url: "/leaderboard/agents", params: search } : "/leaderboard/agents";
}

/** GET /leaderboard/agents, injected into the bench API so the shared endpoint list stays untouched. */
export const agentLeaderboardApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getAgentLeaderboard: builder.query<AgentLeaderboardResponse, AgentLeaderboardParams | void>({
			query: (params) => agentLeaderboardRequest(params ?? {}),
			providesTags: ["BenchLeaderboard"],
		}),
	}),
});

export const { useGetAgentLeaderboardQuery } = agentLeaderboardApi;
