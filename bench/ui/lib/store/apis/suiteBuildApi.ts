import type { RowImportRequest, RowImportResult, RowImportStatus, SuiteGraderRequest, SuiteGraderResult, TaskSplitResult } from "@/lib/types/suiteBuild";
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
		getRowImport: builder.query<RowImportStatus, { suite: string; id: string }>({
			query: ({ suite, id }) => `/suites/${seg(suite)}/rows/imports/${seg(id)}`,
		}),
		setSuiteGrader: builder.mutation<SuiteGraderResult, { suite: string } & SuiteGraderRequest>({
			query: ({ suite, ...body }) => ({ url: `/suites/${seg(suite)}/grader`, method: "PUT", body }),
			invalidatesTags: (_r, _e, { suite }) => suiteTags(suite),
		}),
		setTaskSplit: builder.mutation<TaskSplitResult, { suite: string; task: string; held_out: boolean }>({
			query: ({ suite, task, held_out }) => ({ url: `/suites/${seg(suite)}/tasks/${seg(task)}/split`, method: "PUT", body: { held_out } }),
			invalidatesTags: (_r, _e, { suite }) => suiteTags(suite),
			// N118: flip the row and the "N of M held out" count in the cached list as soon as the PUT
			// succeeds, so the page never waits on the list refetch to show the new state.
			async onQueryStarted({ suite, task }, { dispatch, queryFulfilled }) {
				try {
					const { data } = await queryFulfilled;
					dispatch(
						benchApi.util.updateQueryData("getSuites", undefined, (draft) => {
							const row = draft.suites.find((x) => x.id === suite)?.tasks.find((x) => x.id === task);
							if (row) row.split = data.split;
						}),
					);
				} catch {
					// The error shows on the row (useSetTaskSplitMutation state); the cache stays as it was.
				}
			},
		}),
	}),
});

export const { useImportSuiteRowsMutation, useGetRowImportQuery, useSetSuiteGraderMutation, useSetTaskSplitMutation } = suiteBuildApi;
