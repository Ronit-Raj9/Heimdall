import type { InboxWebhookInfo, PullReadiness } from "@/lib/types/liveinbox";
import { benchApi } from "./benchApi";

/** Web parity lane L18: the inbox webhook setup read; G03: pull readiness. Live and inbox jobs go through POST /jobs. */
export const liveinboxApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchInboxWebhook", "BenchPullReady"] }).injectEndpoints({
	endpoints: (builder) => ({
		getInboxWebhook: builder.query<InboxWebhookInfo, void>({
			query: () => "/inbox/webhook",
			providesTags: [{ type: "BenchInboxWebhook", id: "INFO" }],
		}),
		getPullReadiness: builder.query<PullReadiness, void>({
			query: () => "/liveinbox/pull",
			providesTags: [{ type: "BenchPullReady", id: "INFO" }],
		}),
	}),
});

export const { useGetInboxWebhookQuery, useGetPullReadinessQuery } = liveinboxApi;
