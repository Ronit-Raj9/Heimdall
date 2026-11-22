import type {
	AgentConfigDoc,
	AgentConfigPut,
	AgentConfigValidateRequest,
	AgentConfigValidateResponse,
	AgentFromTemplateRequest,
	AgentFromTemplateResponse,
	AgentTemplateList,
	EndpointPreview,
	EndpointTestRequest,
	EndpointTestResult,
} from "@/lib/types/agentBuilder";
import { benchApi } from "./benchApi";

/**
 * Agent builder routes (web parity L08): the template catalog, create from a template, and the
 * config editor. Creating or saving an agent refreshes the agents list and its versions.
 */
export const agentBuilderApi = benchApi.enhanceEndpoints({ addTagTypes: ["AgentConfig", "AgentVersions"] }).injectEndpoints({
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
			// A save can make a new agent version, so the Versions tab reloads too.
			invalidatesTags: (_r, _e, arg) => [{ type: "AgentConfig", id: arg.id }, { type: "AgentVersions", id: arg.id }, "BenchAgents"],
		}),
		/** Sends one sample task to an endpoint agent (J3); waits up to about two minutes. */
		testEndpoint: builder.mutation<EndpointTestResult, EndpointTestRequest>({
			query: (body) => ({ url: "/agents/test-endpoint", method: "POST", body, timeout: 130_000 }),
		}),
		/** Renders the requests a sample task would send (nothing is sent). */
		previewEndpoint: builder.mutation<EndpointPreview, EndpointTestRequest>({
			query: (body) => ({ url: "/agents/endpoint-preview", method: "POST", body }),
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
	useTestEndpointMutation,
	usePreviewEndpointMutation,
} = agentBuilderApi;