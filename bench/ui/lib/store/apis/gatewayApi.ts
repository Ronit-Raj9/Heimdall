import type {
	GwBifrostSettingAnswer,
	GwDraftRequest,
	GwDraftResponse,
	GwEffect,
	GwFeature,
	GwRegisteredAgent,
	GwReportCard,
	GwSourceInfo,
	GwWatchState,
} from "@/lib/types/gateway";
import { benchApi } from "./benchApi";

const enc = encodeURIComponent;

/** The Bifrost feature test routes (audit/W4_DESIGN_3_BIFROST.md 3.6, 6.6, 14.4, 16.3, 17.3). */
export const gatewayApi = benchApi.enhanceEndpoints({ addTagTypes: ["Gateway"] }).injectEndpoints({
	endpoints: (builder) => ({
		getGatewayFeatures: builder.query<{ features: GwFeature[] }, void>({
			query: () => "/gateway/features",
			providesTags: [{ type: "Gateway", id: "FEATURES" }],
		}),
		draftGatewayTest: builder.mutation<GwDraftResponse, GwDraftRequest>({
			query: (body) => ({ url: "/gateway/tests/draft", method: "POST", body }),
		}),
		getGatewayEffect: builder.query<GwEffect, string>({
			query: (expId) => `/experiments/${enc(expId)}/gateway-effect`,
			providesTags: (_r, _e, expId) => [{ type: "Gateway", id: `effect:${expId}` }],
		}),
		getGatewayReportCard: builder.query<GwReportCard, void>({
			query: () => "/gateway/report-card",
			providesTags: [{ type: "Gateway", id: "CARD" }],
		}),
		setGatewaySource: builder.mutation<GwSourceInfo, { gateway_url: string; credential_secret: string }>({
			query: (body) => ({ url: "/gateway/source", method: "POST", body }),
			invalidatesTags: [{ type: "Gateway", id: "CARD" }],
		}),
		removeGatewaySource: builder.mutation<GwSourceInfo, void>({
			query: () => ({ url: "/gateway/source/remove", method: "POST" }),
			invalidatesTags: [{ type: "Gateway", id: "CARD" }],
		}),
		getBifrostSetting: builder.query<GwBifrostSettingAnswer, string>({
			query: (expId) => `/experiments/${enc(expId)}/bifrost-setting`,
			providesTags: (_r, _e, expId) => [{ type: "Gateway", id: `setting:${expId}` }],
		}),
		getGatewayAgents: builder.query<{ agents: GwRegisteredAgent[] }, void>({
			query: () => "/gateway/agents",
			providesTags: [{ type: "Gateway", id: "AGENTS" }],
		}),
		importGatewayAgent: builder.mutation<
			{ agent_id: string; yaml: string },
			{ name: string; model: string; provider: string; id?: string }
		>({
			query: ({ name, ...body }) => ({ url: `/gateway/agents/${enc(name)}/import`, method: "POST", body }),
		}),
		createAgentFromYaml: builder.mutation<{ id: string }, { yaml: string }>({
			query: (body) => ({ url: "/agents", method: "POST", body }),
			invalidatesTags: ["BenchAgents"],
		}),
		getModelWatch: builder.query<GwWatchState, string>({
			query: (schedule) => `/gateway/model-watch/${enc(schedule)}`,
			providesTags: (_r, _e, schedule) => [{ type: "Gateway", id: `watch:${schedule}` }],
		}),
		retryModelWatch: builder.mutation<GwWatchState, { schedule: string; model: string }>({
			query: ({ schedule, model }) => ({ url: `/gateway/model-watch/${enc(schedule)}/retry`, method: "POST", body: { model } }),
			invalidatesTags: (_r, _e, arg) => [{ type: "Gateway", id: `watch:${arg.schedule}` }],
		}),
	}),
});

export const {
	useDraftGatewayTestMutation,
	useGetGatewayEffectQuery,
	useGetGatewayReportCardQuery,
	useSetGatewaySourceMutation,
	useRemoveGatewaySourceMutation,
	useGetBifrostSettingQuery,
	useGetGatewayAgentsQuery,
	useImportGatewayAgentMutation,
	useCreateAgentFromYamlMutation,
	useGetModelWatchQuery,
	useRetryModelWatchMutation,
} = gatewayApi;