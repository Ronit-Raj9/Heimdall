import type {
	ContinueOffer,
	ContinueRequest,
	ImprovementDetail,
	ImprovementEstimate,
	ImprovementList,
	ImprovementSummary,
	VersionExplain,
} from "@/lib/types/improve";
import type { FetchArgs } from "@reduxjs/toolkit/query/react";
import { benchApi } from "./benchApi";

/**
 * The body of POST /improvements: the fields of improve.Spec the web form sets. The server fills
 * every other field with its defaults (improve/spec.go) and parses the body strictly.
 */
export interface ImprovementSpecBody {
	kind: "improvement";
	name: string;
	/** agents/<id>.yaml */
	agent: string;
	/** <suite id>@<version> */
	suite: string;
	rounds?: number;
	/** small, medium, large or custom (A3.6): the preset fills every field the body leaves out. */
	preset: string;
	/** Container paths of the agent's files map (exact files, and folders ending in "/"), and the standing instructions. */
	editable: { files: string[]; dirs?: string[]; instructions: boolean };
	/** The improver task (the default reflect-edit task) and, for a custom size, its ideas per round. */
	improver: { task: string; candidates?: number; keep?: number };
	/** The whole run's hard stop in dollars (A2.10). */
	budget: { total_usd: number };
}

/** Both the run and the list change when a run is resumed or continued. */
const runAndList = (id: string) => [
	{ type: "BenchImprovement" as const, id: "LIST" },
	{ type: "BenchImprovement" as const, id },
];

/**
 * Improvement runs (bench/improve). Reads take no token; approve and stop are writes and carry the
 * write token like every bench mutation. Both refresh the list and the run so the page can poll.
 */
export const improveApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchImprovement"] }).injectEndpoints({
	endpoints: (builder) => ({
		getImprovements: builder.query<ImprovementList, void>({
			query: () => "/improvements",
			providesTags: [{ type: "BenchImprovement", id: "LIST" }],
		}),
		getImprovement: builder.query<ImprovementDetail, string>({
			query: (id) => improvementPath(id),
			providesTags: (_r, _e, id) => [{ type: "BenchImprovement", id }],
		}),
		// POST /improvements (admins): the spec as JSON; the server freezes it and starts the run (202).
		createImprovement: builder.mutation<ImprovementSummary, ImprovementSpecBody>({
			query: (body) => ({ url: "/improvements", method: "POST", body }),
			invalidatesTags: [{ type: "BenchImprovement", id: "LIST" }],
		}),
		// POST /improvements/estimate (viewers): the create body; no side effects (A2.9).
		estimateImprovement: builder.mutation<ImprovementEstimate, ImprovementSpecBody>({
			query: (body) => ({ url: "/improvements/estimate", method: "POST", body }),
		}),
		// POST /improvements/{id}/resume (admins): continues an interrupted, failed or planned run (202).
		resumeImprovement: builder.mutation<ImprovementSummary, string>({
			query: (id) => ({ url: `${improvementPath(id)}/resume`, method: "POST" }),
			invalidatesTags: (_r, _e, id) => runAndList(id),
		}),
		// POST /improvements/{id}/continue (admins): one second look with more budget (202).
		continueImprovement: builder.mutation<ImprovementSummary, { id: string } & ContinueRequest>({
			query: ({ id, ...body }) => ({ url: `${improvementPath(id)}/continue`, method: "POST", body }),
			invalidatesTags: (_r, _e, a) => runAndList(a.id),
		}),
		// POST /improvements/{id}/continue/estimate (viewers): what a second look costs; nothing is spent.
		continueEstimate: builder.mutation<ContinueOffer, { id: string } & ContinueRequest>({
			query: ({ id, ...body }) => ({ url: `${improvementPath(id)}/continue/estimate`, method: "POST", body }),
		}),
		// GET /improvements/{id}/versions/{n}/explain (viewers): what changed in version n and why (A4.3).
		getVersionExplain: builder.query<VersionExplain, { id: string; n: number }>({
			query: ({ id, n }) => `${improvementPath(id)}/versions/${n}/explain`,
			providesTags: (_r, _e, a) => [{ type: "BenchImprovement", id: a.id }],
		}),
		approveImprovementRound: builder.mutation<ImprovementSummary, { id: string; round: number; reason: string }>({
			query: (a) => approveRequest(a.id, a.round, a.reason),
			invalidatesTags: (_r, _e, a) => [
				{ type: "BenchImprovement", id: "LIST" },
				{ type: "BenchImprovement", id: a.id },
			],
		}),
		stopImprovement: builder.mutation<ImprovementSummary, string>({
			query: (id) => ({ url: `${improvementPath(id)}/stop`, method: "POST" }),
			invalidatesTags: (_r, _e, id) => [
				{ type: "BenchImprovement", id: "LIST" },
				{ type: "BenchImprovement", id },
			],
		}),
	}),
});

/** One improvement's path, the id escaped as one segment. */
export function improvementPath(id: string): string {
	return `/improvements/${encodeURIComponent(id)}`;
}

/** POST /improvements/{id}/rounds/{k}/approve with the reason. */
export function approveRequest(id: string, round: number, reason: string): FetchArgs {
	return { url: `${improvementPath(id)}/rounds/${round}/approve`, method: "POST", body: { reason } };
}

export const {
	useGetImprovementsQuery,
	useGetImprovementQuery,
	useCreateImprovementMutation,
	useApproveImprovementRoundMutation,
	useStopImprovementMutation,
	useEstimateImprovementMutation,
	useResumeImprovementMutation,
	useContinueImprovementMutation,
	useContinueEstimateMutation,
	useGetVersionExplainQuery,
} = improveApi;