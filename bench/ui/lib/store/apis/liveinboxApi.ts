import type { InboxTokenCreated, InboxTokenList, InboxWebhookInfo, PullReadiness } from "@/lib/types/liveinbox";
import { benchApi } from "./benchApi";

/**
 * Web parity lane L18: the inbox webhook setup read. Live and inbox jobs go through POST /jobs.
 * Gap G02: the hosted per-org ingest tokens (list for viewers, create and revoke for admins).
 * Gap G03: pull readiness.
 */
export const liveinboxApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchInboxWebhook", "BenchInboxTokens", "BenchPullReady"] }).injectEndpoints({
	endpoints: (builder) => ({
		getInboxWebhook: builder.query<InboxWebhookInfo, void>({
			query: () => "/inbox/webhook",
			providesTags: [{ type: "BenchInboxWebhook", id: "INFO" }],
		}),
		getInboxTokens: builder.query<InboxTokenList, void>({
			query: () => "/inbox/tokens",
			providesTags: [{ type: "BenchInboxTokens", id: "LIST" }],
		}),
		createInboxToken: builder.mutation<InboxTokenCreated, { label?: string }>({
			query: (body) => ({ url: "/inbox/tokens", method: "POST", body }),
			invalidatesTags: [{ type: "BenchInboxTokens", id: "LIST" }],
		}),
		revokeInboxToken: builder.mutation<void, string>({
			query: (id) => ({ url: `/inbox/tokens/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: [{ type: "BenchInboxTokens", id: "LIST" }],
		}),
		getPullReadiness: builder.query<PullReadiness, void>({
			query: () => "/liveinbox/pull",
			providesTags: [{ type: "BenchPullReady", id: "INFO" }],
		}),
	}),
});

export const { useGetInboxWebhookQuery, useGetInboxTokensQuery, useCreateInboxTokenMutation, useRevokeInboxTokenMutation, useGetPullReadinessQuery } = liveinboxApi;
