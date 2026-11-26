import type {
	AnnotationAgreement,
	AnnotationItemList,
	AnnotationItemsRequest,
	AnnotationItemsResult,
	AnnotationLabel,
	AnnotationLabelResult,
	AnnotationNext,
	AnnotationPeople,
	AnnotationQueue,
	AnnotationQueueList,
	AnnotationQueueRequest,
	AnnotationState,
	DataPolicyRequest,
	DataPolicyView,
	DatasetVersionList,
	ImportPlatformList,
	PlatformImportRequest,
	PlatformImportResult,
} from "@/lib/types/annotation";
import { benchApi } from "./benchApi";

const seg = (v: string) => encodeURIComponent(v);
const queuePath = (id: string) => `/annotation/queues/${seg(id)}`;

type Tag = "AnnotationQueues" | "BenchData";

/** Tags a label, decision or new item touches: the queue list, the queue, its items and its agreement. */
export function queueTags(id: string): { type: Tag; id: string }[] {
	return [
		{ type: "AnnotationQueues", id: "LIST" },
		{ type: "AnnotationQueues", id },
		{ type: "AnnotationQueues", id: `${id}/items` },
		{ type: "AnnotationQueues", id: `${id}/agreement` },
	];
}

/**
 * Lane Y15: annotation queues (people label runs or judge decisions; agreeing labels resolve an
 * item and write a judge calibration label), a benchmark's data versions and data policy, and
 * imports from LangSmith, Langfuse, Braintrust and Datadog.
 */
export const annotationApi = benchApi.enhanceEndpoints({ addTagTypes: ["AnnotationQueues", "BenchData"] }).injectEndpoints({
	endpoints: (builder) => ({
		getAnnotationQueues: builder.query<AnnotationQueueList, void>({
			query: () => "/annotation/queues",
			providesTags: [{ type: "AnnotationQueues", id: "LIST" }],
		}),
		getAnnotationQueue: builder.query<AnnotationQueue, string>({
			query: (id) => queuePath(id),
			providesTags: (_r, _e, id) => [{ type: "AnnotationQueues", id }],
		}),
		createAnnotationQueue: builder.mutation<AnnotationQueue, AnnotationQueueRequest>({
			query: (body) => ({ url: "/annotation/queues", method: "POST", body }),
			invalidatesTags: [{ type: "AnnotationQueues", id: "LIST" }],
		}),
		updateAnnotationQueue: builder.mutation<AnnotationQueue, { id: string } & AnnotationQueueRequest>({
			query: ({ id, ...body }) => ({ url: queuePath(id), method: "PUT", body }),
			invalidatesTags: (_r, _e, { id }) => queueTags(id),
		}),
		deleteAnnotationQueue: builder.mutation<unknown, string>({
			query: (id) => ({ url: queuePath(id), method: "DELETE" }),
			invalidatesTags: [{ type: "AnnotationQueues", id: "LIST" }],
		}),
		addAnnotationItems: builder.mutation<AnnotationItemsResult, { id: string } & AnnotationItemsRequest>({
			query: ({ id, ...body }) => ({ url: `${queuePath(id)}/items`, method: "POST", body }),
			invalidatesTags: (_r, _e, { id }) => queueTags(id),
		}),
		getAnnotationItems: builder.query<AnnotationItemList, { id: string; state?: AnnotationState; limit?: number }>({
			query: ({ id, state, limit }) => ({ url: `${queuePath(id)}/items`, params: { ...(state ? { state } : {}), limit: limit ?? 200 } }),
			providesTags: (_r, _e, { id }) => [{ type: "AnnotationQueues", id: `${id}/items` }],
		}),
		assignAnnotationItems: builder.mutation<{ assigned: number }, string>({
			query: (id) => ({ url: `${queuePath(id)}/assign`, method: "POST" }),
			invalidatesTags: (_r, _e, id) => queueTags(id),
		}),
		// "Next" reserves an item for the caller, so it is a mutation, never cached.
		// With skip, the server passes on those items and frees their reservations (wave 4, p12-3).
		nextAnnotationItem: builder.mutation<AnnotationNext, string | { id: string; skip?: string[] }>({
			query: (arg) => {
				const { id, skip } = typeof arg === "string" ? { id: arg, skip: undefined } : arg;
				return { url: `${queuePath(id)}/next`, method: "POST", ...(skip && skip.length > 0 ? { body: { skip } } : {}) };
			},
		}),
		labelAnnotationItem: builder.mutation<AnnotationLabelResult, { id: string; item: string; label: AnnotationLabel; note?: string }>({
			query: ({ id, item, label, note }) => ({
				url: `${queuePath(id)}/items/${seg(item)}/labels`,
				method: "POST",
				body: note && note.trim() ? { label, note: note.trim() } : { label },
			}),
			invalidatesTags: (_r, _e, { id }) => queueTags(id),
		}),
		resolveAnnotationItem: builder.mutation<AnnotationLabelResult, { id: string; item: string; label: AnnotationLabel; note?: string }>({
			query: ({ id, item, label, note }) => ({
				url: `${queuePath(id)}/items/${seg(item)}/resolve`,
				method: "POST",
				body: note && note.trim() ? { label, note: note.trim() } : { label },
			}),
			invalidatesTags: (_r, _e, { id }) => queueTags(id),
		}),
		// The members a queue can name as labellers (N168); names only, never emails.
		getAnnotationPeople: builder.query<AnnotationPeople, void>({
			query: () => "/annotation/people",
		}),
		getAnnotationAgreement: builder.query<AnnotationAgreement, string>({
			query: (id) => `${queuePath(id)}/agreement`,
			providesTags: (_r, _e, id) => [{ type: "AnnotationQueues", id: `${id}/agreement` }],
		}),

		getDatasetVersions: builder.query<DatasetVersionList, string>({
			query: (suite) => `/suites/${seg(suite)}/dataset/versions`,
			providesTags: (_r, _e, suite) => [{ type: "BenchData", id: `${suite}/versions` }],
		}),
		getDataPolicy: builder.query<DataPolicyView, string>({
			query: (suite) => `/suites/${seg(suite)}/data-policy`,
			providesTags: (_r, _e, suite) => [{ type: "BenchData", id: `${suite}/policy` }],
		}),
		setDataPolicy: builder.mutation<DataPolicyView, { suite: string } & DataPolicyRequest>({
			query: ({ suite, ...body }) => ({ url: `/suites/${seg(suite)}/data-policy`, method: "PUT", body }),
			invalidatesTags: (_r, _e, { suite }) => [{ type: "BenchData", id: `${suite}/policy` }],
		}),
		getImportPlatforms: builder.query<ImportPlatformList, void>({
			query: () => "/imports/platforms",
		}),
		importFromPlatform: builder.mutation<PlatformImportResult, PlatformImportRequest>({
			query: (body) => ({ url: "/imports/platforms", method: "POST", body }),
			invalidatesTags: (r, _e, { benchmark, dry_run }) =>
				dry_run || !r || !benchmark
					? []
					: [
							{ type: "BenchData", id: `${benchmark}/versions` },
							{ type: "BenchSuites" as never, id: "LIST" },
						],
		}),
	}),
});

export const {
	useGetAnnotationQueuesQuery,
	useGetAnnotationQueueQuery,
	useCreateAnnotationQueueMutation,
	useUpdateAnnotationQueueMutation,
	useDeleteAnnotationQueueMutation,
	useAddAnnotationItemsMutation,
	useGetAnnotationItemsQuery,
	useAssignAnnotationItemsMutation,
	useNextAnnotationItemMutation,
	useLabelAnnotationItemMutation,
	useResolveAnnotationItemMutation,
	useGetAnnotationAgreementQuery,
	useGetAnnotationPeopleQuery,
	useGetDatasetVersionsQuery,
	useGetDataPolicyQuery,
	useSetDataPolicyMutation,
	useGetImportPlatformsQuery,
	useImportFromPlatformMutation,
} = annotationApi;