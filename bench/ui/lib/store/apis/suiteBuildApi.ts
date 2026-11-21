import type { RowImportRequest, RowImportResult, SuiteGraderRequest, SuiteGraderResult, TaskSplitResult } from "@/lib/types/suiteBuild";
import { benchApi } from "./benchApi";

const seg = (v: string) => encodeURIComponent(v);
const suiteTags = (suite: string) => [
	{ type: "BenchSuites" as const, id: "LIST" },
	{ type: "BenchSuites" as const, id: `${suite}/health` },
];

/** Own benchmarks (lane X13): import rows and tickets, choose the grader, mark tasks held out. */
export const suiteBuildApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		importSuiteRows: builder.mutation<RowImportResult, { suite: string } & RowImportRequest>({
			query: ({ suite, ...body }) => ({ url: `/suites/${seg(suite)}/rows`, method: "POST", body }),
			invalidatesTags: (r, _e, { suite, dry_run }) => (dry_run || !r ? [] : suiteTags(suite)),
		}),
		setSuiteGrader: builder.mutation<SuiteGraderResult, { suite: string } & SuiteGraderRequest>({
			query: ({ suite, ...body }) => ({ url: `/suites/${seg(suite)}/grader`, method: "PUT", body }),
			invalidatesTags: (_r, _e, { suite }) => suiteTags(suite),
		}),
		setTaskSplit: builder.mutation<TaskSplitResult, { suite: string; task: string; held_out: boolean }>({
			query: ({ suite, task, held_out }) => ({ url: `/suites/${seg(suite)}/tasks/${seg(task)}/split`, method: "PUT", body: { held_out } }),
			invalidatesTags: (_r, _e, { suite }) => suiteTags(suite),
		}),
	}),
});

export const { useImportSuiteRowsMutation, useSetSuiteGraderMutation, useSetTaskSplitMutation } = suiteBuildApi;
