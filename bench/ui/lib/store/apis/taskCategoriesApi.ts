import { benchApi } from "./benchApi";

/** The kinds of work a task can have (taskcat.All on the server, in display order). */
export const TASK_CATEGORIES = [
	"bug_fix",
	"feature",
	"refactor",
	"test_repair",
	"dependency_update",
	"performance",
	"docs",
	"navigation",
	"tool_use",
	"policy",
	"conversation",
] as const;

/** One task's label (taskcat.Label). */
export interface TaskCategoryLabel {
	category: string;
	source: string;
	sure: boolean;
	why: string;
	at: string;
}

/** GET /suites/{id}/categories: the labels by task id. */
export interface TaskCategoriesView {
	suite: string;
	labels: Record<string, TaskCategoryLabel>;
	labelled?: number;
}

/** The kind of work of each task of a benchmark (W4-44): read by viewers, set by members. */
export const taskCategoriesApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchTaskCategories"] }).injectEndpoints({
	endpoints: (builder) => ({
		getSuiteCategories: builder.query<TaskCategoriesView, string>({
			query: (suiteId) => `/suites/${encodeURIComponent(suiteId)}/categories`,
			providesTags: (_r, _e, suiteId) => [{ type: "BenchTaskCategories", id: suiteId }],
		}),
		setTaskCategory: builder.mutation<unknown, { suiteId: string; task: string; category: string }>({
			query: ({ suiteId, task, category }) => ({
				url: `/suites/${encodeURIComponent(suiteId)}/tasks/${encodeURIComponent(task)}/category`,
				method: "PUT",
				body: { category },
			}),
			invalidatesTags: (_r, _e, { suiteId }) => [{ type: "BenchTaskCategories", id: suiteId }],
		}),
	}),
});

export const { useGetSuiteCategoriesQuery, useSetTaskCategoryMutation } = taskCategoriesApi;