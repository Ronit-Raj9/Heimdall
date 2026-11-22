import type { TraceConnectInfo, TraceInferResult } from "@/lib/types/connectTraces";
import { benchApi } from "./benchApi";

/**
 * Connect from traces (handlers/connecttraces.go): what the org's OTLP traces show so far, and an
 * agent inferred from one export file. Nothing here creates the agent; the page does that with
 * POST /agents/from-template.
 */
export const connectTracesApi = benchApi.enhanceEndpoints({ addTagTypes: ["ConnectTraces"] }).injectEndpoints({
	endpoints: (builder) => ({
		getConnectTraces: builder.query<TraceConnectInfo, void>({
			query: () => "/connect/traces",
			providesTags: ["ConnectTraces"],
		}),
		forgetConnectTraces: builder.mutation<{ forgotten: boolean }, void>({
			query: () => ({ url: "/connect/traces", method: "DELETE" }),
			invalidatesTags: ["ConnectTraces"],
		}),
		inferFromTraceFile: builder.mutation<TraceInferResult, { body: string | ArrayBuffer; protobuf: boolean }>({
			query: ({ body, protobuf }) => ({
				url: "/connect/traces/infer",
				method: "POST",
				body,
				headers: { "Content-Type": protobuf ? "application/x-protobuf" : "application/json" },
			}),
		}),
	}),
});

export const { useGetConnectTracesQuery, useForgetConnectTracesMutation, useInferFromTraceFileMutation } = connectTracesApi;

export { connectRequest, withToken } from "./connectTracesHelpers";
