import type {
	AgentConfigDoc,
	AgentConfigPut,
	AgentConfigValidateRequest,
	AgentConfigValidateResponse,
	AgentFromTemplateRequest,
	AgentFromTemplateResponse,
	AgentTemplateList,
} from "@/lib/types/agentBuilder";
import { benchApi } from "./benchApi";

/**
 * Agent builder routes (web parity L08): the template catalog, create from a template, and the
 * config editor. Creating or saving an agent refreshes the agents list.
 */
export const agentBuilderApi = benchApi.enhanceEndpoints({ addTagTypes: ["AgentConfig"] }).injectEndpoints({
	endpoints: (builder) => ({
		getAgentTemplates: builder.query<AgentTemplateList, void>({
			query: () => "/agents/templates",
		}),
		createAgentFromTemplate: builder.mutation<AgentFromTemplateResponse, AgentFromTemplateRequest>({
			query: (body) => ({ url: "/agents/from-template", method: "POST", body }),
			invalidatesTags: (_r, _e, arg) => (arg.dry_run ? [] : ["BenchAgents"]),
		}),
		getAgentConfig: builder.query<AgentConfigDoc, string>({
			query: (id) => `/agents/${encodeURIComponent(id)}/config`,
			providesTags: (_r, _e, id) => [{ type: "AgentConfig", id }],
		}),
		putAgentConfig: builder.mutation<AgentConfigDoc, AgentConfigPut>({
			query: ({ id, ...body }) => ({ url: `/agents/${encodeURIComponent(id)}/config`, method: "PUT", body }),
			invalidatesTags: (_r, _e, arg) => [{ type: "AgentConfig", id: arg.id }, "BenchAgents"],
		}),
		validateAgentConfig: builder.mutation<AgentConfigValidateResponse, AgentConfigValidateRequest>({
			query: (body) => ({ url: "/agents/config/validate", method: "POST", body }),
		}),
	}),
});

export const {
	useGetAgentTemplatesQuery,
	useCreateAgentFromTemplateMutation,
	useGetAgentConfigQuery,
	usePutAgentConfigMutation,
	useValidateAgentConfigMutation,
} = agentBuilderApi;
