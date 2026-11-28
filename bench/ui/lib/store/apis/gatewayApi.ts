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

/** The effect in an answer, or null for {"effect": null}, an empty answer or anything that is no effect. Pure. */
export function effectOf(answer: unknown): GwEffect | null {
	if (!answer || typeof answer !== "object") return null;
	if ("effect" in answer) {
		const inner = (answer as { effect: unknown }).effect;
		if (inner === null || inner === undefined) return null;
		return effectOf(inner);
	}
	const e = answer as Partial<GwEffect>;
	return typeof e.feature === "string" && typeof e.state === "string" ? (answer as GwEffect) : null;
}

/** The Bifrost feature test routes (audit/W4_DESIGN_3_BIFROST.md 3.6, 6.6, 14.4, 16.3, 17.3). */
export const gatewayApi = benchApi.enhanceEndpoints({ addTagTypes: ["Gateway"] }).injectEndpoints({
	endpoints: (builder) => ({
		/** With an agent path, the overhead test's need uses that agent's measured calls per run. */
		getGatewayFeatures: builder.query<{ features: GwFeature[] }, string | void>({
			query: (base) => (base ? `/gateway/features?base=${encodeURIComponent(base)}` : "/gateway/features"),
			providesTags: [{ type: "Gateway", id: "FEATURES" }],
		}),
		draftGatewayTest: builder.mutation<GwDraftResponse, GwDraftRequest>({
			query: (body) => ({ url: "/gateway/tests/draft", method: "POST", body }),
		}),
		getGatewayEffect: builder.query<GwEffect, string>({
			query: (expId) => `/experiments/${enc(expId)}/gateway-effect`,
			providesTags: (_r, _e, expId) => [{ type: "Gateway", id: `effect:${expId}` }],
		}),
		// M4-7: with ?optional=1 a comparison that is no feature test answers {"effect": null} instead of
		// a 404. The answer is read with effectOf, so the hook gives the effect or null; an older server
		// still answers 404 for no effect, read as nothing by the caller.
		getGatewayEffectOptional: builder.query<GwEffect | null, string>({
			query: (expId) => `/experiments/${enc(expId)}/gateway-effect?optional=1`,
			transformResponse: (raw: unknown) => effectOf(raw),
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
	useGetGatewayFeaturesQuery,
	useGetGatewayEffectQuery,
	useGetGatewayEffectOptionalQuery,
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