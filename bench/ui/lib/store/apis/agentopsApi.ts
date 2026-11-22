import type { AgentMCPRequest, AgentMCPResponse, AgentSkillsResponse, SkillScanResult } from "@/lib/types/agentops";
import { benchApi } from "./benchApi";

const enc = encodeURIComponent;

/** Agent ops area (web parity lane L09): skills and MCP tools of one agent. */
export const agentopsApi = benchApi.enhanceEndpoints({ addTagTypes: ["AgentSkills", "AgentMCP"] }).injectEndpoints({
	endpoints: (builder) => ({
		getAgentSkills: builder.query<AgentSkillsResponse, string>({
			query: (id) => `/agents/${enc(id)}/skills`,
			providesTags: (_r, _e, id) => [{ type: "AgentSkills", id }],
		}),
		/** Upload answers the scan's verdict: the skill is installed only when the scan admits it. */
		uploadAgentSkill: builder.mutation<SkillScanResult, { id: string; name: string; file: Blob; replace?: boolean }>({
			query: ({ id, name, file, replace }) => ({
				url: `/agents/${enc(id)}/skills`,
				method: "POST",
				params: replace ? { name, replace: "true" } : { name },
				body: file,
				headers: { "Content-Type": "application/zip" },
			}),
			invalidatesTags: (_r, _e, { id }) => [{ type: "AgentSkills", id }, "BenchAgents"],
		}),
		/** Scan one skill where it is: a blocked installed skill is uninstalled, an admitted blocked one installed. */
		scanAgentSkill: builder.mutation<SkillScanResult, { id: string; name: string }>({
			query: ({ id, name }) => ({ url: `/agents/${enc(id)}/skills/${enc(name)}/scan`, method: "POST" }),
			invalidatesTags: (_r, _e, { id }) => [{ type: "AgentSkills", id }, "BenchAgents"],
		}),
		deleteAgentSkill: builder.mutation<AgentSkillsResponse, { id: string; name: string }>({
			query: ({ id, name }) => ({ url: `/agents/${enc(id)}/skills/${enc(name)}`, method: "DELETE" }),
			invalidatesTags: (_r, _e, { id }) => [{ type: "AgentSkills", id }],
		}),
		getAgentMCP: builder.query<AgentMCPResponse, string>({
			query: (id) => `/agents/${enc(id)}/mcp`,
			providesTags: (_r, _e, id) => [{ type: "AgentMCP", id }],
		}),
		putAgentMCP: builder.mutation<AgentMCPResponse, { id: string; body: AgentMCPRequest }>({
			query: ({ id, body }) => ({ url: `/agents/${enc(id)}/mcp`, method: "PUT", body }),
			invalidatesTags: (_r, _e, { id }) => [{ type: "AgentMCP", id }, "BenchAgents"],
		}),
	}),
});

export const {
	useGetAgentSkillsQuery,
	useUploadAgentSkillMutation,
	useScanAgentSkillMutation,
	useDeleteAgentSkillMutation,
	useGetAgentMCPQuery,
	usePutAgentMCPMutation,
} = agentopsApi;
