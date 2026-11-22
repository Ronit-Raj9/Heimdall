// Issues to benchmark (lane G06): org routes under /issuebench/runs.
import type { IssueRetryBody, IssueRun } from "@/lib/types/issuebench";
import { benchApi } from "./benchApi";

const runPath = (id: string) => `/issuebench/runs/${encodeURIComponent(id)}`;

export const issuebenchApi = benchApi.enhanceEndpoints({ addTagTypes: ["IssueRun"] }).injectEndpoints({
	endpoints: (builder) => ({
		getIssueRun: builder.query<IssueRun, string>({
			query: (id) => runPath(id),
			providesTags: (_r, _e, id) => [{ type: "IssueRun", id }],
		}),
		scanIssues: builder.mutation<IssueRun, { repo: string; limit?: number }>({
			query: (body) => ({ url: "/issuebench/runs", method: "POST", body }),
		}),
		startIssueRun: builder.mutation<IssueRun, { id: string; consent: boolean }>({
			query: ({ id, consent }) => ({ url: `${runPath(id)}/start`, method: "POST", body: { consent } }),
			invalidatesTags: (_r, _e, a) => [{ type: "IssueRun", id: a.id }],
		}),
		reviewIssue: builder.mutation<IssueRun, { id: string; issue: number; decision: "accept" | "reject" | "edit"; instruction?: string }>({
			query: ({ id, issue, ...body }) => ({ url: `${runPath(id)}/issues/${issue}/review`, method: "POST", body }),
			invalidatesTags: (_r, _e, a) => [{ type: "IssueRun", id: a.id }],
		}),
		retryIssue: builder.mutation<IssueRun, { id: string; issue: number } & IssueRetryBody>({
			query: ({ id, issue, ...body }) => ({ url: `${runPath(id)}/issues/${issue}/retry`, method: "POST", body }),
			invalidatesTags: (_r, _e, a) => [{ type: "IssueRun", id: a.id }],
		}),
	}),
});

export const { useGetIssueRunQuery, useScanIssuesMutation, useStartIssueRunMutation, useReviewIssueMutation, useRetryIssueMutation } = issuebenchApi;
