import { benchApi } from "./benchApi";

/**
 * DELETE /agents/{id} (lane F13): removes the agent's settings file. Saved versions and runs stay.
 * The server refuses built-in agents and agents with an evaluation still running (409).
 */
export const agentDeleteApi = benchApi.enhanceEndpoints({ addTagTypes: ["AgentConfig"] }).injectEndpoints({
	endpoints: (builder) => ({
		deleteAgent: builder.mutation<void, string>({
			query: (id) => ({ url: `/agents/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: (_r, error, id) => (error ? [] : ["BenchAgents", { type: "AgentConfig", id }]),
		}),
	}),
});

export const { useDeleteAgentMutation } = agentDeleteApi;