import type { AppRule, AppTasksCreateRequest, AppTasksDraft, AppTasksDraftSummary, AppTasksWriteResult } from "@/lib/types/appTasks";
import { benchApi } from "./benchApi";

const seg = (v: string) => encodeURIComponent(v);

/** Tasks from your API (lane Z18): drafts, rule approval, writing and review. The model steps are the apptasks.generate job. */
export const appTasksApi = benchApi.enhanceEndpoints({ addTagTypes: ["AppTasks"] }).injectEndpoints({
	endpoints: (builder) => ({
		getAppTaskDrafts: builder.query<{ drafts: AppTasksDraftSummary[] }, void>({
			query: () => "/app-tasks",
			providesTags: [{ type: "AppTasks", id: "LIST" }],
		}),
		getAppTaskDraft: builder.query<AppTasksDraft, string>({
			query: (id) => `/app-tasks/${seg(id)}`,
			providesTags: (_r, _e, id) => [{ type: "AppTasks", id }],
		}),
		createAppTaskDraft: builder.mutation<AppTasksDraft, AppTasksCreateRequest>({
			query: (body) => ({ url: "/app-tasks", method: "POST", body }),
			invalidatesTags: [{ type: "AppTasks", id: "LIST" }],
		}),
		approveAppTaskRules: builder.mutation<AppTasksDraft, { id: string; rules: AppRule[] }>({
			query: ({ id, rules }) => ({ url: `/app-tasks/${seg(id)}/rules`, method: "PUT", body: { rules, approve: true } }),
			invalidatesTags: (_r, _e, { id }) => [
				{ type: "AppTasks", id },
				{ type: "AppTasks", id: "LIST" },
			],
		}),
		writeAppTasks: builder.mutation<AppTasksWriteResult, { id: string; judge?: { provider: string; model: string }; held_out?: boolean }>({
			query: ({ id, ...body }) => ({ url: `/app-tasks/${seg(id)}/write`, method: "POST", body }),
			invalidatesTags: (_r, _e, { id }) => [
				{ type: "AppTasks", id },
				{ type: "AppTasks", id: "LIST" },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
		reviewAppTask: builder.mutation<unknown, { id: string; task: string; decision: "accepted" | "rejected" }>({
			query: ({ id, ...body }) => ({ url: `/app-tasks/${seg(id)}/review`, method: "POST", body }),
			invalidatesTags: (_r, _e, { id }) => [
				{ type: "AppTasks", id },
				{ type: "AppTasks", id: "LIST" },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
	}),
});

export const {
	useGetAppTaskDraftsQuery,
	useGetAppTaskDraftQuery,
	useCreateAppTaskDraftMutation,
	useApproveAppTaskRulesMutation,
	useWriteAppTasksMutation,
	useReviewAppTaskMutation,
} = appTasksApi;