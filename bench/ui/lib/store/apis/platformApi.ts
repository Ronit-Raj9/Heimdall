// Hosted platform routes (docs/platform-design.md): auth, organizations, members, invitations,
// API tokens, runners, the audit log and jobs. Injected into benchApi so they share its base query
// (CSRF header, 401 handling) and its cache.
import type {
	ApiTokenCreatedResponse,
	ApiTokenListResponse,
	ApiTokenRequest,
	AuditListResponse,
	AuditLogParams,
	AuthConfigResponse,
	AuthMeResponse,
	ExperimentResponse,
	InvitationListResponse,
	InvitationPreviewResponse,
	InvitationRequest,
	InvitationResponse,
	JobListResponse,
	JobLogsResponse,
	JobResponse,
	LoginRequest,
	MeOrgResponse,
	MemberListResponse,
	MemberResponse,
	OrgListResponse,
	OrgResponse,
	Role,
	RunnerListResponse,
	RunnerTokenCreatedResponse,
	RunnerTokenListResponse,
	RunnerTokenRequest,
	RunnerTokenResponse,
	SignupRequest,
	SwitchOrgRequest,
} from "@/lib/types/bench";
import { benchApi } from "./benchApi";
import { BENCH_API_BASE } from "./fixtures";
import { errorStatus, setAuthState } from "./session";

const id = (value: string) => encodeURIComponent(value);

/** How often the jobs drawer and indicator refresh while a job is queued or running. */
export const JOBS_POLL_MS = 3000;
/** How often a job page asks for new log lines when the stream is not available. */
export const JOB_LOG_POLL_MS = 1500;

/** Browser navigation target that starts GitHub sign-in; the server redirects back to return_to. */
export function githubStartUrl(returnTo: string): string {
	return `${BENCH_API_BASE}/auth/github/start?return_to=${encodeURIComponent(returnTo)}`;
}

/** Server-sent events URL of a job's log. */
export function jobLogStreamUrl(jobId: string, since = 0): string {
	return `${BENCH_API_BASE}/jobs/${id(jobId)}/logs?since=${since}`;
}

/** The runner install script this server serves (GET /runners/install.sh), for a fresh Linux box. */
export const RUNNER_INSTALL_SCRIPT = `${BENCH_API_BASE}/runners/install.sh`;
/** The runner guide in the repository. */
export const RUNNER_INSTALL_DOC = "https://github.com/maximhq/bifrost/blob/main/bench/docs/runners.md";

// Every tag of data that belongs to one organization: switching organization refetches all of it.
const ORG_TAGS = [
	"BenchHealth",
	"BenchExperiment",
	"BenchVerdict",
	"BenchDiagnostics",
	"BenchGrid",
	"BenchRun",
	"BenchRunEvents",
	"BenchRunDiff",
	"BenchRunReceipts",
	"BenchPair",
	"BenchRelease",
	"BenchTriage",
	"BenchLeaderboard",
	"BenchSuites",
	"BenchAgents",
	"BenchImports",
	"BenchWorkers",
	"BenchReviews",
	"BenchMatrix",
	"BenchClusters",
	"AuthMe",
	"Members",
	"Invitations",
	"ApiTokens",
	"Runners",
	"RunnerTokens",
	"AuditLog",
	"Jobs",
] as const;

/** GET /orgs/{org}/audit request: empty filters left out so the server defaults apply. */
export function auditLogRequest({ orgId, ...params }: AuditLogParams): string | { url: string; params: Record<string, string | number> } {
	const url = `/orgs/${encodeURIComponent(orgId)}/audit`;
	const search = Object.fromEntries(Object.entries(params).filter(([, value]) => value !== undefined && value !== "" && value !== 0));
	return Object.keys(search).length > 0 ? { url, params: search as Record<string, string | number> } : url;
}

export const platformApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		// Who is calling. 404 (a server before the platform) and auth_mode local both mean local mode.
		getMe: builder.query<AuthMeResponse, void>({
			query: () => "/auth/me",
			providesTags: ["AuthMe"],
			async onQueryStarted(_arg, { queryFulfilled }) {
				try {
					const { data } = await queryFulfilled;
					setAuthState({ mode: data.auth_mode === "hosted" ? "hosted" : "local", csrf: data.csrf });
				} catch (failure) {
					const status = errorStatus((failure as { error?: unknown })?.error);
					// Only a hosted server answers /auth/me with 401; anything else leaves local mode on.
					setAuthState({ mode: status === 401 ? "hosted" : "local" });
				}
			},
		}),
		// One anonymous read of an org route: it answers only when the server serves a public demo
		// to visitors who are not signed in. Its own cache entry, so data read while signed in never
		// passes for a demo after the session ends.
		probeDemo: builder.query<boolean, void>({
			query: () => "/experiments",
			transformResponse: () => true,
			keepUnusedDataFor: 0,
		}),
		// What the sign-in page offers (sign-up policy, GitHub); answered to anyone.
		getAuthConfig: builder.query<AuthConfigResponse, void>({
			query: () => "/auth/config",
		}),
		login: builder.mutation<AuthMeResponse, LoginRequest>({
			query: (body) => ({ url: "/auth/login", method: "POST", body }),
		}),
		signup: builder.mutation<AuthMeResponse, SignupRequest>({
			query: (body) => ({ url: "/auth/signup", method: "POST", body }),
		}),
		logout: builder.mutation<void, void>({
			query: () => ({ url: "/auth/logout", method: "POST" }),
		}),
		switchOrg: builder.mutation<MeOrgResponse, SwitchOrgRequest>({
			query: (body) => ({ url: "/auth/switch-org", method: "POST", body }),
			invalidatesTags: [...ORG_TAGS],
		}),
		getInvitation: builder.query<InvitationPreviewResponse, string>({
			query: (token) => `/auth/invitations/${id(token)}`,
		}),
		acceptInvitation: builder.mutation<MeOrgResponse, string>({
			query: (token) => ({ url: `/auth/invitations/${id(token)}`, method: "POST" }),
			invalidatesTags: [...ORG_TAGS],
		}),

		// Members and invitations of an organization (admin). orgId is the session's org.
		listMembers: builder.query<MemberListResponse, string>({
			query: (orgId) => `/orgs/${id(orgId)}/members`,
			providesTags: ["Members"],
		}),
		updateMember: builder.mutation<MemberResponse, { orgId: string; userId: string; role: Role }>({
			query: ({ orgId, userId, role }) => ({ url: `/orgs/${id(orgId)}/members/${id(userId)}/role`, method: "POST", body: { role } }),
			invalidatesTags: ["Members", "AuditLog"],
		}),
		removeMember: builder.mutation<unknown, { orgId: string; userId: string }>({
			query: ({ orgId, userId }) => ({ url: `/orgs/${id(orgId)}/members/${id(userId)}/remove`, method: "POST" }),
			invalidatesTags: ["Members", "AuditLog"],
		}),
		listInvitations: builder.query<InvitationListResponse, string>({
			query: (orgId) => `/orgs/${id(orgId)}/invitations`,
			providesTags: ["Invitations"],
		}),
		createInvitation: builder.mutation<InvitationResponse, { orgId: string } & InvitationRequest>({
			query: ({ orgId, ...body }) => ({ url: `/orgs/${id(orgId)}/invitations`, method: "POST", body }),
			invalidatesTags: ["Invitations", "AuditLog"],
		}),
		revokeInvitation: builder.mutation<unknown, { orgId: string; invitationId: string }>({
			query: ({ orgId, invitationId }) => ({ url: `/orgs/${id(orgId)}/invitations/${id(invitationId)}/revoke`, method: "POST" }),
			invalidatesTags: ["Invitations", "AuditLog"],
		}),

		// API tokens of the current organization (admin). The secret is in the create answer only.
		listApiTokens: builder.query<ApiTokenListResponse, void>({
			query: () => "/auth/tokens",
			providesTags: ["ApiTokens"],
		}),
		createApiToken: builder.mutation<ApiTokenCreatedResponse, ApiTokenRequest>({
			query: (body) => ({ url: "/auth/tokens", method: "POST", body }),
			invalidatesTags: ["ApiTokens", "AuditLog"],
		}),
		revokeApiToken: builder.mutation<unknown, string>({
			query: (tokenId) => ({ url: `/auth/tokens/${id(tokenId)}/revoke`, method: "POST" }),
			invalidatesTags: ["ApiTokens", "AuditLog"],
		}),

		// Runners and runner tokens of the caller's organization (admin).
		listRunners: builder.query<RunnerListResponse, void>({
			query: () => "/runners",
			providesTags: ["Runners"],
		}),
		removeRunner: builder.mutation<unknown, string>({
			query: (runnerId) => ({ url: `/runners/${id(runnerId)}/remove`, method: "POST" }),
			invalidatesTags: ["Runners", "AuditLog"],
		}),
		listRunnerTokens: builder.query<RunnerTokenListResponse, void>({
			query: () => "/runners/tokens",
			providesTags: ["RunnerTokens"],
		}),
		createRunnerToken: builder.mutation<RunnerTokenCreatedResponse, RunnerTokenRequest>({
			query: (body) => ({ url: "/runners/tokens", method: "POST", body }),
			invalidatesTags: ["RunnerTokens", "AuditLog"],
		}),
		revokeRunnerToken: builder.mutation<RunnerTokenResponse, string>({
			query: (tokenId) => ({ url: `/runners/tokens/${id(tokenId)}/revoke`, method: "POST" }),
			invalidatesTags: ["RunnerTokens", "Runners", "AuditLog"],
		}),

		// Audit log (admin), newest first, one page at a time (before = unix ms of the last row).
		getAuditLog: builder.query<AuditListResponse, AuditLogParams>({
			query: (params) => auditLogRequest(params),
			providesTags: ["AuditLog"],
		}),

		// Organizations: the user's list, rename (admin), public read (owner).
		listOrgs: builder.query<OrgListResponse, void>({
			query: () => "/orgs",
			providesTags: ["AuthMe"],
		}),
		getOrg: builder.query<OrgResponse, string>({
			query: (orgId) => `/orgs/${id(orgId)}`,
			providesTags: ["AuthMe"],
		}),
		renameOrg: builder.mutation<OrgResponse, { orgId: string; name: string }>({
			query: ({ orgId, name }) => ({ url: `/orgs/${id(orgId)}/rename`, method: "POST", body: { name } }),
			invalidatesTags: ["AuthMe", "AuditLog"],
		}),
		setPublicRead: builder.mutation<OrgResponse, { orgId: string; publicRead: boolean }>({
			query: ({ orgId, publicRead }) => ({ url: `/orgs/${id(orgId)}/public-read`, method: "POST", body: { public_read: publicRead } }),
			invalidatesTags: ["AuthMe", "AuditLog"],
		}),

		// Jobs of the caller's organization.
		listJobs: builder.query<JobListResponse, void>({
			query: () => "/jobs",
			providesTags: [{ type: "Jobs", id: "LIST" }],
		}),
		getJob: builder.query<JobResponse, string>({
			query: (jobId) => `/jobs/${id(jobId)}`,
			providesTags: (_result, _error, jobId) => [{ type: "Jobs", id: jobId }],
		}),
		getJobLogs: builder.query<JobLogsResponse, { jobId: string; since: number }>({
			query: ({ jobId, since }) => ({ url: `/jobs/${id(jobId)}/logs`, params: { since } }),
		}),
		cancelJob: builder.mutation<JobResponse, string>({
			query: (jobId) => ({ url: `/jobs/${id(jobId)}/cancel`, method: "POST" }),
			invalidatesTags: (_result, _error, jobId) => [
				{ type: "Jobs", id: jobId },
				{ type: "Jobs", id: "LIST" },
			],
		}),

		// Experiment run control (handlers/experimentjobs.go): 202 with the experiment's status.
		cancelExperiment: builder.mutation<ExperimentResponse, string>({
			query: (expId) => ({ url: `/experiments/${id(expId)}/cancel`, method: "POST" }),
			invalidatesTags: (_result, _error, expId) => [
				{ type: "BenchExperiment", id: expId },
				{ type: "BenchExperiment", id: "LIST" },
				{ type: "Jobs", id: "LIST" },
			],
		}),
		resumeExperiment: builder.mutation<ExperimentResponse, string>({
			query: (expId) => ({ url: `/experiments/${id(expId)}/resume`, method: "POST" }),
			invalidatesTags: (_result, _error, expId) => [
				{ type: "BenchExperiment", id: expId },
				{ type: "BenchExperiment", id: "LIST" },
				{ type: "Jobs", id: "LIST" },
			],
		}),
	}),
});

export const {
	useGetMeQuery,
	useGetAuthConfigQuery,
	useProbeDemoQuery,
	useLoginMutation,
	useSignupMutation,
	useLogoutMutation,
	useSwitchOrgMutation,
	useGetInvitationQuery,
	useAcceptInvitationMutation,
	useListMembersQuery,
	useUpdateMemberMutation,
	useRemoveMemberMutation,
	useListInvitationsQuery,
	useCreateInvitationMutation,
	useRevokeInvitationMutation,
	useListApiTokensQuery,
	useCreateApiTokenMutation,
	useRevokeApiTokenMutation,
	useListRunnersQuery,
	useRemoveRunnerMutation,
	useListRunnerTokensQuery,
	useCreateRunnerTokenMutation,
	useRevokeRunnerTokenMutation,
	useGetAuditLogQuery,
	useListOrgsQuery,
	useGetOrgQuery,
	useRenameOrgMutation,
	useSetPublicReadMutation,
	useListJobsQuery,
	useGetJobQuery,
	useGetJobLogsQuery,
	useCancelJobMutation,
	useCancelExperimentMutation,
	useResumeExperimentMutation,
} = platformApi;