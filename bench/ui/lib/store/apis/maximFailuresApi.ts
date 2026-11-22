import type {
	MaximDraftRequest,
	MaximDraftResult,
	MaximFailureBatch,
	MaximFailureFilter,
	MaximFailureList,
	MaximReviewRequest,
	MaximTaskResult,
} from "@/lib/types/maximFailures";
import { benchApi } from "./benchApi";

const base = "/integrations/maxim/batches";

function taskPath(batch: string, suite: string, task: string): string {
	return `${base}/${encodeURIComponent(batch)}/tasks/${encodeURIComponent(suite)}/${encodeURIComponent(task)}`;
}

/** Maxim production failures to regression tests (lane Z06). */
export const maximFailuresApi = benchApi.enhanceEndpoints({ addTagTypes: ["MaximBatch"] }).injectEndpoints({
	endpoints: (builder) => ({
		listMaximBatches: builder.query<MaximFailureList, void>({
			query: () => base,
			providesTags: [{ type: "MaximBatch", id: "LIST" }],
		}),
		getMaximBatch: builder.query<MaximFailureBatch, string>({
			query: (id) => `${base}/${encodeURIComponent(id)}`,
			providesTags: (_r, _e, id) => [{ type: "MaximBatch", id }],
		}),
		pullMaximFailures: builder.mutation<MaximFailureBatch, MaximFailureFilter>({
			query: (filter) => ({ url: base, method: "POST", body: { filter } }),
			invalidatesTags: [{ type: "MaximBatch", id: "LIST" }],
		}),
		draftMaximTasks: builder.mutation<MaximDraftResult, { batch: string } & MaximDraftRequest>({
			query: ({ batch, ...body }) => ({ url: `${base}/${encodeURIComponent(batch)}/draft`, method: "POST", body }),
			invalidatesTags: (_r, _e, a) => [{ type: "MaximBatch", id: a.batch }, { type: "MaximBatch", id: "LIST" }],
		}),
		acceptMaximTask: builder.mutation<MaximTaskResult, { batch: string; suite: string; task: string } & MaximReviewRequest>({
			query: ({ batch, suite, task, ...body }) => ({ url: `${taskPath(batch, suite, task)}/accept`, method: "POST", body }),
			invalidatesTags: (_r, _e, a) => [{ type: "MaximBatch", id: a.batch }, { type: "MaximBatch", id: "LIST" }],
		}),
		rejectMaximTask: builder.mutation<MaximTaskResult, { batch: string; suite: string; task: string; reason: string }>({
			query: ({ batch, suite, task, reason }) => ({ url: `${taskPath(batch, suite, task)}/reject`, method: "POST", body: { reason } }),
			invalidatesTags: (_r, _e, a) => [{ type: "MaximBatch", id: a.batch }, { type: "MaximBatch", id: "LIST" }],
		}),
		rerunMaximReadiness: builder.mutation<MaximTaskResult, { batch: string; suite: string; task: string }>({
			query: ({ batch, suite, task }) => ({ url: `${taskPath(batch, suite, task)}/readiness`, method: "POST" }),
			invalidatesTags: (_r, _e, a) => [{ type: "MaximBatch", id: a.batch }],
		}),
	}),
});

export const {
	useListMaximBatchesQuery,
	useGetMaximBatchQuery,
	usePullMaximFailuresMutation,
	useDraftMaximTasksMutation,
	useAcceptMaximTaskMutation,
	useRejectMaximTaskMutation,
	useRerunMaximReadinessMutation,
} = maximFailuresApi;
