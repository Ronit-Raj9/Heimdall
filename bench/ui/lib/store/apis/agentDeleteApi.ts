import type { AgentConfigDoc } from "@/lib/types/agentBuilder";
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
		/**
		 * POST /agents/{id}/copy (fix wave 2): writes the agent's settings under new_id (the copy starts at v1). With
		 * remove_original it renames the agent: the original goes, its runs, results and saved versions keep the old name.
		 */
		copyAgent: builder.mutation<AgentConfigDoc, { id: string; new_id: string; remove_original?: boolean }>({
			query: ({ id, ...body }) => ({ url: `/agents/${encodeURIComponent(id)}/copy`, method: "POST", body }),
			invalidatesTags: (_r, error, { id, new_id, remove_original }) =>
				error
					? []
					: ["BenchAgents", { type: "AgentConfig", id: new_id }, ...(remove_original ? [{ type: "AgentConfig" as const, id }] : [])],
		}),
	}),
});

export const { useDeleteAgentMutation, useCopyAgentMutation } = agentDeleteApi;