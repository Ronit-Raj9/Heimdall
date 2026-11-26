import { benchApi } from "./benchApi";

/** One run of a weekly issue sync (GET /issuesync). */
export interface IssueSyncRun {
	at: string;
	run_id?: string;
	started: number;
	skipped: number;
	spent_usd: number;
	status: "started" | "nothing_new" | "refused" | "failed";
	reason?: string;
}

/** A repository with a weekly issue sync. The record never holds a token. */
export interface IssueSyncRepo {
	id: string;
	repo: string;
	benchmark: string;
	weekday: number;
	hour_utc: number;
	max_issues: number;
	per_issue_usd: number;
	weekly_usd: number;
	model: { provider?: string; model?: string };
	private: boolean;
	paused: boolean;
	paused_reason?: string;
	runs: IssueSyncRun[] | null;
}

/** The body of POST /issuesync and PUT /issuesync/{id}; missing fields keep their value. */
export interface IssueSyncBody {
	repo?: string;
	weekday?: number;
	hour_utc?: number;
	max_issues?: number;
	weekly_usd?: number;
	model?: { provider?: string; model?: string };
	paused?: boolean;
}

/** Weekly issue sync routes (W4-48). Changing a sync is admin work. */
export const issueSyncApi = benchApi.enhanceEndpoints({ addTagTypes: ["IssueSync"] }).injectEndpoints({
	endpoints: (builder) => ({
		listIssueSyncs: builder.query<{ repos: IssueSyncRepo[] }, void>({
			query: () => ({ url: "/issuesync" }),
			providesTags: ["IssueSync"],
		}),
		createIssueSync: builder.mutation<IssueSyncRepo, IssueSyncBody>({
			query: (body) => ({ url: "/issuesync", method: "POST", body }),
			invalidatesTags: (_r, error) => (error ? [] : ["IssueSync"]),
		}),
		updateIssueSync: builder.mutation<IssueSyncRepo, { id: string } & IssueSyncBody>({
			query: ({ id, ...body }) => ({ url: `/issuesync/${encodeURIComponent(id)}`, method: "PUT", body }),
			invalidatesTags: (_r, error) => (error ? [] : ["IssueSync"]),
		}),
		deleteIssueSync: builder.mutation<void, string>({
			query: (id) => ({ url: `/issuesync/${encodeURIComponent(id)}`, method: "DELETE" }),
			invalidatesTags: (_r, error) => (error ? [] : ["IssueSync"]),
		}),
		runIssueSync: builder.mutation<IssueSyncRun, string>({
			query: (id) => ({ url: `/issuesync/${encodeURIComponent(id)}/run`, method: "POST", body: { yes: true } }),
			invalidatesTags: (_r, error) => (error ? [] : ["IssueSync"]),
		}),
	}),
});

export const {
	useListIssueSyncsQuery,
	useCreateIssueSyncMutation,
	useUpdateIssueSyncMutation,
	useDeleteIssueSyncMutation,
	useRunIssueSyncMutation,
} = issueSyncApi;