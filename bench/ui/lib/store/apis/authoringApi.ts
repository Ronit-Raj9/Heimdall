import type { GeneratedTaskList, HeldOutSplitResult, QualityReport, SplitPlan, TaskReviewDecision } from "@/lib/types/authoring";
import { benchApi } from "./benchApi";

const seg = (v: string) => encodeURIComponent(v);
const suiteTags = (suite: string) => [
	{ type: "BenchSuites" as const, id: "LIST" },
	{ type: "BenchSuites" as const, id: `${suite}/health` },
	{ type: "BenchSuites" as const, id: `${suite}/generated` },
];

/** Authoring helper (lane Z19): review generated tasks, quality checks, held-out split. */
export const authoringApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getGeneratedTasks: builder.query<GeneratedTaskList, string>({
			query: (suite) => `/suites/${seg(suite)}/generated`,
			providesTags: (_r, _e, suite) => [{ type: "BenchSuites" as const, id: `${suite}/generated` }],
		}),
		setTaskReview: builder.mutation<
			{ suite: string; task: string; review: string; decision: TaskReviewDecision },
			{ suite: string; task: string; decision: "accepted" | "rejected"; note?: string }
		>({
			query: ({ suite, task, ...body }) => ({ url: `/suites/${seg(suite)}/tasks/${seg(task)}/review-state`, method: "PUT", body }),
			invalidatesTags: (_r, _e, { suite }) => suiteTags(suite),
		}),
		getSuiteQuality: builder.query<QualityReport, string>({
			query: (suite) => `/suites/${seg(suite)}/quality`,
		}),
		splitHeldOut: builder.mutation<HeldOutSplitResult, { suite: string; dry_run?: boolean } & SplitPlan>({
			query: ({ suite, ...body }) => ({ url: `/suites/${seg(suite)}/held-out`, method: "POST", body }),
			invalidatesTags: (r, _e, { suite, dry_run }) => (dry_run || !r ? [] : suiteTags(suite)),
		}),
	}),
});

export const { useGetGeneratedTasksQuery, useSetTaskReviewMutation, useLazyGetSuiteQualityQuery, useSplitHeldOutMutation } = authoringApi;
