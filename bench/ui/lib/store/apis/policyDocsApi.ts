import type { PolicyClause, PolicyDocList, PolicyDocUpload, PolicyDocView, PolicyReport, PolicyReviewRequest, PolicyReviewResult } from "@/lib/types/policyDocs";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";

const seg = (v: string) => encodeURIComponent(v);
const docTags = (suite: string) => [{ type: "BenchSuites" as const, id: `${suite}/policy-docs` }];

/** Download link of the compliance report (Markdown or PDF), served by the API as an attachment. */
export function policyReportUrl(suite: string, doc: string, format: "markdown" | "pdf"): string {
	return `${BENCH_API_BASE}/suites/${seg(suite)}/policy-docs/${seg(doc)}/report?format=${format}`;
}

/** Policy documents of a benchmark (lane Z23). */
export const policyDocsApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		listPolicyDocs: builder.query<PolicyDocList, string>({
			query: (suite) => `/suites/${seg(suite)}/policy-docs`,
			providesTags: (_r, _e, suite) => docTags(suite),
		}),
		getPolicyDoc: builder.query<PolicyDocView, { suite: string; doc: string }>({
			query: ({ suite, doc }) => `/suites/${seg(suite)}/policy-docs/${seg(doc)}`,
			providesTags: (_r, _e, { suite }) => docTags(suite),
		}),
		uploadPolicyDoc: builder.mutation<PolicyDocView, { suite: string } & PolicyDocUpload>({
			query: ({ suite, ...body }) => ({ url: `/suites/${seg(suite)}/policy-docs`, method: "POST", body }),
			invalidatesTags: (r, _e, { suite, dry_run }) => (dry_run || !r ? [] : docTags(suite)),
		}),
		reviewPolicyDrafts: builder.mutation<PolicyReviewResult, { suite: string; doc: string } & PolicyReviewRequest>({
			query: ({ suite, doc, ...body }) => ({ url: `/suites/${seg(suite)}/policy-docs/${seg(doc)}/review`, method: "POST", body }),
			invalidatesTags: (r, _e, { suite }) => (r ? [...docTags(suite), { type: "BenchSuites" as const, id: "LIST" }] : []),
		}),
		setPolicyClauses: builder.mutation<PolicyDocView, { suite: string; doc: string; clauses: PolicyClause[] }>({
			query: ({ suite, doc, clauses }) => ({ url: `/suites/${seg(suite)}/policy-docs/${seg(doc)}/clauses`, method: "PUT", body: { clauses } }),
			invalidatesTags: (r, _e, { suite }) => (r ? docTags(suite) : []),
		}),
		deletePolicyDoc: builder.mutation<void, { suite: string; doc: string }>({
			query: ({ suite, doc }) => ({ url: `/suites/${seg(suite)}/policy-docs/${seg(doc)}`, method: "DELETE" }),
			invalidatesTags: (_r, e, { suite }) => (e ? [] : docTags(suite)),
		}),
		getPolicyReport: builder.query<PolicyReport, { suite: string; doc: string }>({
			query: ({ suite, doc }) => `/suites/${seg(suite)}/policy-docs/${seg(doc)}/report`,
			providesTags: (_r, _e, { suite }) => docTags(suite),
		}),
	}),
});

export const {
	useListPolicyDocsQuery,
	useGetPolicyDocQuery,
	useUploadPolicyDocMutation,
	useReviewPolicyDraftsMutation,
	useGetPolicyReportQuery,
	useSetPolicyClausesMutation,
	useDeletePolicyDocMutation,
} = policyDocsApi;
