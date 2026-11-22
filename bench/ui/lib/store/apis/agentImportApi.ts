import type { AgentImportRequest, AgentImportResponse, AgentPlatformList } from "@/lib/types/agentImport";
import { benchApi } from "./benchApi";

/**
 * Import an agent by its platform id (lane Z16): the platform catalog, and the import itself
 * (dry_run reads the platform and shows the config without saving it). Saving refreshes the
 * agents list.
 */
export const agentImportApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getAgentPlatforms: builder.query<AgentPlatformList, void>({
			query: () => "/agents/platforms",
		}),
		importAgentFromPlatform: builder.mutation<AgentImportResponse, AgentImportRequest>({
			query: (body) => ({ url: "/agents/import-platform", method: "POST", body, timeout: 60_000 }),
			invalidatesTags: (_r, _e, arg) => (arg.dry_run ? [] : ["BenchAgents"]),
		}),
	}),
});

export const { useGetAgentPlatformsQuery, useImportAgentFromPlatformMutation } = agentImportApi;
