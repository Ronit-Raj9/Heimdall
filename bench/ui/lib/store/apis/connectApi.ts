import type {
	ConnectCatalog,
	ConnectDetectRequest,
	ConnectDetectResponse,
	ConnectFirstLookResponse,
	ConnectSaveRequest,
	ConnectSaveResponse,
	ConnectVerifyRequest,
	ConnectVerifyStatus,
} from "@/lib/types/connect";
import { benchApi } from "./benchApi";

/**
 * The Connect box (lane Z01, bench/handlers/connect.go): detect what was pasted, save it as an agent,
 * verify it row by row (the agent.verify job), then plan the first look.
 */
export const connectApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getConnectCatalog: builder.query<ConnectCatalog, void>({
			query: () => "/connect/failures",
		}),
		detectAgent: builder.mutation<ConnectDetectResponse, ConnectDetectRequest>({
			query: (body) => ({ url: "/connect/detect", method: "POST", body }),
		}),
		saveConnectedAgent: builder.mutation<ConnectSaveResponse, ConnectSaveRequest>({
			query: (body) => ({ url: "/connect/agents", method: "POST", body }),
		}),
		startVerify: builder.mutation<ConnectVerifyStatus, ConnectVerifyRequest>({
			query: (body) => ({ url: "/connect/verify", method: "POST", body }),
		}),
		getVerify: builder.query<ConnectVerifyStatus, string>({
			query: (id) => `/connect/verify/${encodeURIComponent(id)}`,
		}),
		planFirstLook: builder.mutation<ConnectFirstLookResponse, { agent: string; model?: string; provider?: string }>({
			// estimate=1 adds the cost range of the first look (M2-85); an older server ignores it.
			query: (body) => ({ url: "/connect/first-look?estimate=1", method: "POST", body }),
		}),
	}),
});

export const {
	useGetConnectCatalogQuery,
	useDetectAgentMutation,
	useSaveConnectedAgentMutation,
	useStartVerifyMutation,
	useGetVerifyQuery,
	usePlanFirstLookMutation,
} = connectApi;