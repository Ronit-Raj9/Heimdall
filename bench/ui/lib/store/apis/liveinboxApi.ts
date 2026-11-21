import type { InboxWebhookInfo } from "@/lib/types/liveinbox";
import { benchApi } from "./benchApi";

/** Web parity lane L18: the inbox webhook setup read. Live and inbox jobs go through POST /jobs. */
export const liveinboxApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchInboxWebhook"] }).injectEndpoints({
	endpoints: (builder) => ({
		getInboxWebhook: builder.query<InboxWebhookInfo, void>({
			query: () => "/inbox/webhook",
			providesTags: [{ type: "BenchInboxWebhook", id: "INFO" }],
		}),
	}),
});

export const { useGetInboxWebhookQuery } = liveinboxApi;
