// GitHub repository connection (lane L07): hosted platform routes under /github. The OAuth
// token stays on the server; these endpoints only ever see metadata.
import type {
	GithubCIInstallRequest,
	GithubCIInstallResult,
	GithubRefsResponse,
	GithubReposResponse,
	GithubStatus,
} from "@/lib/types/github";
import { benchApi } from "./benchApi";

const seg = (value: string) => encodeURIComponent(value);

/** Request path of one repository's branches and tags. */
export function githubRefsPath(owner: string, repo: string): string {
	return `/github/repos/${seg(owner)}/${seg(repo)}/refs`;
}

export const githubApi = benchApi.enhanceEndpoints({ addTagTypes: ["GithubStatus", "GithubRepos"] }).injectEndpoints({
	endpoints: (builder) => ({
		getGithubStatus: builder.query<GithubStatus, void>({
			query: () => "/github/status",
			providesTags: ["GithubStatus"],
		}),
		startGithubConnect: builder.mutation<{ url: string }, { return_to?: string } | void>({
			query: (body) => ({ url: "/github/connect/start", method: "POST", body: body ?? {} }),
		}),
		disconnectGithub: builder.mutation<GithubStatus, void>({
			query: () => ({ url: "/github/disconnect", method: "POST" }),
			invalidatesTags: ["GithubStatus", "GithubRepos"],
		}),
		listGithubRepos: builder.query<GithubReposResponse, number | void>({
			query: (page) => `/github/repos?page=${page ?? 1}`,
			providesTags: ["GithubRepos"],
		}),
		getGithubRefs: builder.query<GithubRefsResponse, { owner: string; repo: string }>({
			query: ({ owner, repo }) => githubRefsPath(owner, repo),
		}),
		installGithubCI: builder.mutation<GithubCIInstallResult, GithubCIInstallRequest>({
			query: (body) => ({ url: "/github/ci-install", method: "POST", body }),
		}),
	}),
});

export const {
	useGetGithubStatusQuery,
	useStartGithubConnectMutation,
	useDisconnectGithubMutation,
	useListGithubReposQuery,
	useGetGithubRefsQuery,
	useInstallGithubCIMutation,
} = githubApi;
