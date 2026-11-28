import type { DatasetTaskList, DatasetTasksRequest, DatasetTasksResult, DatasetVersionDetail, McpServerReport } from "@/lib/types/ufl5";
import type { IssueRun } from "@/lib/types/issuebench";
import { benchApi } from "./benchApi";

const seg = (v: string) => encodeURIComponent(v);

/** GET /mcp-servers/benchmarks/{id}/report (handlers/mcptest.go). */
export function mcpServerReportPath(benchmark: string): string {
	return `/mcp-servers/benchmarks/${seg(benchmark)}/report`;
}

/** GET /suites/{id}/dataset/tasks?offset&limit (handlers/datasetapi.go listTasks). */
export function datasetTasksRequest(suite: string, offset: number, limit: number) {
	return { url: `/suites/${seg(suite)}/dataset/tasks`, params: { offset, limit } };
}

/** POST /suites/{id}/dataset/tasks: add (mode add) or replace (mode upsert, the row keeps its id). */
export function datasetWriteRequest(suite: string, body: DatasetTasksRequest) {
	return { url: `/suites/${seg(suite)}/dataset/tasks`, method: "POST" as const, body };
}

/** DELETE /suites/{id}/dataset/tasks/{task}. */
export function datasetDeleteRequest(suite: string, task: string) {
	return { url: `/suites/${seg(suite)}/dataset/tasks/${seg(task)}`, method: "DELETE" as const };
}

/** GET /suites/{id}/dataset/versions/{n}. */
export function datasetVersionPath(suite: string, n: number): string {
	return `/suites/${seg(suite)}/dataset/versions/${n}`;
}

/** POST /issuebench/runs/{id}/cancel (handlers/issuebench.go): no body. */
export function issueCancelRequest(id: string) {
	return { url: `/issuebench/runs/${seg(id)}/cancel`, method: "POST" as const };
}

/** POST /redteam/regressions/harvest (handlers.RedTeamHarvestRequest). */
export function harvestRequest(evaluation: string) {
	return { url: "/redteam/regressions/harvest", method: "POST" as const, body: { evaluation } };
}

/** Round 5 lane L5: the MCP server report and the rows of a question-and-answer benchmark. */
export const ufl5Api = benchApi.enhanceEndpoints({ addTagTypes: ["BenchData", "BenchSuites", "IssueRun", "BenchRedTeam"] }).injectEndpoints({
	endpoints: (builder) => ({
		getMcpServerReport: builder.query<McpServerReport, string>({
			query: (benchmark) => mcpServerReportPath(benchmark),
		}),
		listDatasetTasks: builder.query<DatasetTaskList, { suite: string; offset: number; limit: number }>({
			query: ({ suite, offset, limit }) => datasetTasksRequest(suite, offset, limit),
			providesTags: (_r, _e, a) => [{ type: "BenchData", id: `${a.suite}/rows` }],
		}),
		writeDatasetTasks: builder.mutation<DatasetTasksResult, { suite: string } & DatasetTasksRequest>({
			query: ({ suite, ...body }) => datasetWriteRequest(suite, body),
			invalidatesTags: (_r, _e, a) => [
				{ type: "BenchData", id: `${a.suite}/rows` },
				{ type: "BenchData", id: `${a.suite}/versions` },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
		deleteDatasetTask: builder.mutation<unknown, { suite: string; task: string }>({
			query: ({ suite, task }) => datasetDeleteRequest(suite, task),
			invalidatesTags: (_r, _e, a) => [
				{ type: "BenchData", id: `${a.suite}/rows` },
				{ type: "BenchData", id: `${a.suite}/versions` },
				{ type: "BenchSuites", id: "LIST" },
			],
		}),
		getDatasetVersion: builder.query<DatasetVersionDetail, { suite: string; n: number }>({
			query: ({ suite, n }) => datasetVersionPath(suite, n),
		}),
		listIssueRuns: builder.query<{ runs: IssueRun[] | null }, void>({
			query: () => "/issuebench/runs",
			providesTags: [{ type: "IssueRun", id: "LIST" }],
		}),
		cancelIssueRun: builder.mutation<IssueRun, string>({
			query: (id) => issueCancelRequest(id),
			invalidatesTags: (_r, _e, id) => [
				{ type: "IssueRun", id },
				{ type: "IssueRun", id: "LIST" },
			],
		}),
		harvestRedTeam: builder.mutation<{ evaluation: string; broken: number; added: { task: string }[] | null }, string>({
			query: (evaluation) => harvestRequest(evaluation),
			invalidatesTags: ["BenchRedTeam"],
		}),
	}),
});

export const {
	useGetMcpServerReportQuery,
	useListDatasetTasksQuery,
	useWriteDatasetTasksMutation,
	useDeleteDatasetTaskMutation,
	useGetDatasetVersionQuery,
	useListIssueRunsQuery,
	useCancelIssueRunMutation,
	useHarvestRedTeamMutation,
} = ufl5Api;
