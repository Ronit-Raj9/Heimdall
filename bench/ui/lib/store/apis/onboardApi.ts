import type {
	OnboardDetectResult,
	OnboardDraftSet,
	OnboardDraftSummary,
	OnboardInput,
	OnboardInventory,
	OnboardPlan,
	OnboardTaskEdit,
} from "@/lib/types/onboard";
import type { Job } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

const seg = (v: string) => encodeURIComponent(v);

/** Add benchmark (lane Z20): detect, inspect, draft, readiness, publish. */
export const onboardApi = benchApi.enhanceEndpoints({ addTagTypes: ["OnboardDrafts"] }).injectEndpoints({
	endpoints: (builder) => ({
		uploadOnboardFile: builder.mutation<{ upload: string; size: number; zip: boolean }, File>({
			query: (file) => ({
				url: `/onboard/upload?filename=${seg(file.name)}`,
				method: "POST",
				body: file,
				headers: { "Content-Type": "application/octet-stream" },
			}),
		}),
		detectOnboard: builder.mutation<OnboardDetectResult, { input: OnboardInput }>({
			query: (body) => ({ url: "/onboard/detect", method: "POST", body }),
		}),
		inspectOnboard: builder.mutation<OnboardInventory, { input: OnboardInput; source?: string }>({
			query: (body) => ({ url: "/onboard/inspect", method: "POST", body }),
		}),
		createOnboardDraft: builder.mutation<OnboardDraftSet, { input: OnboardInput; plan: OnboardPlan }>({
			query: (body) => ({ url: "/onboard/drafts", method: "POST", body }),
			invalidatesTags: [{ type: "OnboardDrafts", id: "LIST" }],
		}),
		listOnboardDrafts: builder.query<{ drafts: OnboardDraftSummary[] }, void>({
			query: () => "/onboard/drafts",
			providesTags: [{ type: "OnboardDrafts", id: "LIST" }],
		}),
		getOnboardDraft: builder.query<OnboardDraftSet, string>({
			query: (id) => `/onboard/drafts/${seg(id)}`,
			providesTags: (_r, _e, id) => [{ type: "OnboardDrafts", id }],
		}),
		editOnboardTask: builder.mutation<OnboardDraftSet, { set: string; task: string } & OnboardTaskEdit>({
			query: ({ set, task, ...body }) => ({ url: `/onboard/drafts/${seg(set)}/tasks/${seg(task)}`, method: "PATCH", body }),
			invalidatesTags: (_r, _e, { set }) => [{ type: "OnboardDrafts", id: set }],
		}),
		startOnboardReadiness: builder.mutation<{ set: string; job: Job }, { set: string; task?: string; judge_consent?: boolean }>({
			query: ({ set, task, judge_consent }) => ({
				url: `/onboard/drafts/${seg(set)}/ready`,
				method: "POST",
				body: { ...(task ? { task } : {}), ...(judge_consent ? { judge_consent } : {}) },
			}),
			invalidatesTags: (_r, _e, { set }) => [{ type: "OnboardDrafts", id: set }],
		}),
		publishOnboardDraft: builder.mutation<{ set: string; job: Job }, { set: string; tasks?: string[]; include_unready?: boolean }>({
			query: ({ set, ...body }) => ({ url: `/onboard/drafts/${seg(set)}/publish`, method: "POST", body }),
			invalidatesTags: (_r, _e, { set }) => [
				{ type: "OnboardDrafts", id: set },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
		deleteOnboardDraft: builder.mutation<void, string>({
			query: (id) => ({ url: `/onboard/drafts/${seg(id)}`, method: "DELETE" }),
			invalidatesTags: [{ type: "OnboardDrafts", id: "LIST" }],
		}),
	}),
});

export const {
	useUploadOnboardFileMutation,
	useDetectOnboardMutation,
	useInspectOnboardMutation,
	useCreateOnboardDraftMutation,
	useListOnboardDraftsQuery,
	useGetOnboardDraftQuery,
	useEditOnboardTaskMutation,
	useStartOnboardReadinessMutation,
	usePublishOnboardDraftMutation,
	useDeleteOnboardDraftMutation,
} = onboardApi;