import type {
	InboxAcceptRequest,
	InboxAcceptResponse,
	InboxActionResponse,
	InboxBulkAcceptRequest,
	InboxBulkAcceptResult,
	InboxItemDetailResponse,
	InboxListParams,
	InboxListResponse,
	InboxRejectRequest,
	InboxSnoozeRequest,
} from "@/lib/types/bench";
import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

/**
 * The failure inbox (docs/failure-inbox.md), injected into the bench API so the shared endpoint
 * list stays untouched. Reads take no token; accept, reject and snooze are writes, so they carry
 * the write token like every bench mutation (prepareBenchHeaders). Every write refreshes the
 * lists and the item; an accept also drafts a task, so the suites list is refreshed too.
 */
export const inboxApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchInbox"] }).injectEndpoints({
	endpoints: (builder) => ({
		getInbox: builder.query<InboxListResponse, InboxListParams | void>({
			query: (params) => inboxListRequest(params ?? {}),
			providesTags: [{ type: "BenchInbox", id: "LIST" }],
		}),
		getInboxItem: builder.query<InboxItemDetailResponse, string>({
			query: (itemId) => inboxItemPath(itemId),
			providesTags: (_result, _error, itemId) => [{ type: "BenchInbox", id: itemId }],
		}),
		acceptInboxItem: builder.mutation<InboxAcceptResponse, { id: string; body: InboxAcceptRequest }>({
			query: ({ id, body }) => inboxActionRequest(id, "accept", body),
			invalidatesTags: (_result, _error, { id }) => [
				{ type: "BenchInbox", id: "LIST" },
				{ type: "BenchInbox", id },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
		bulkAcceptInboxItems: builder.mutation<InboxBulkAcceptResult, InboxBulkAcceptRequest>({
			query: (body) => ({ url: "/inbox/accept", method: "POST", body }),
			invalidatesTags: (_result, _error, { ids }) => [
				{ type: "BenchInbox", id: "LIST" },
				...ids.map((id) => ({ type: "BenchInbox" as const, id })),
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
		rejectInboxItem: builder.mutation<InboxActionResponse, { id: string; body: InboxRejectRequest }>({
			query: ({ id, body }) => inboxActionRequest(id, "reject", body),
			invalidatesTags: (_result, _error, { id }) => [
				{ type: "BenchInbox", id: "LIST" },
				{ type: "BenchInbox", id },
			],
		}),
		snoozeInboxItem: builder.mutation<InboxActionResponse, { id: string; body: InboxSnoozeRequest }>({
			query: ({ id, body }) => inboxActionRequest(id, "snooze", body),
			invalidatesTags: (_result, _error, { id }) => [
				{ type: "BenchInbox", id: "LIST" },
				{ type: "BenchInbox", id },
			],
		}),
	}),
});

/** The GET /inbox request: params with a value, or the bare route when none has one. */
export function inboxListRequest(params: InboxListParams): string | { url: string; params: Record<string, string> } {
	const search = Object.fromEntries(
		Object.entries(params).filter((entry): entry is [string, string] => typeof entry[1] === "string" && entry[1] !== ""),
	);
	return Object.keys(search).length > 0 ? { url: "/inbox", params: search } : "/inbox";
}

/** One item's path, with the id escaped as one path segment. */
export function inboxItemPath(itemId: string): string {
	return `/inbox/${encodeURIComponent(itemId)}`;
}

/** A write on one item: POST /inbox/{id}/accept|reject|snooze with a JSON body. */
export function inboxActionRequest(
	itemId: string,
	action: "accept" | "reject" | "snooze",
	body: InboxAcceptRequest | InboxRejectRequest | InboxSnoozeRequest,
): FetchArgs {
	return { url: `${inboxItemPath(itemId)}/${action}`, method: "POST", body };
}

export const {
	useGetInboxQuery,
	useGetInboxItemQuery,
	useAcceptInboxItemMutation,
	useBulkAcceptInboxItemsMutation,
	useRejectInboxItemMutation,
	useSnoozeInboxItemMutation,
} = inboxApi;