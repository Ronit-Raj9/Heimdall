import type { ConnectVerifyRequest } from "@/lib/types/connect";
import type { AgentFileCheckRequest, GitHubAgentFile, AgentFileCheckResult, ConnectExplainRequest, ConnectExplainResponse } from "@/lib/types/ufl6Connect";
import { benchApi } from "./benchApi";

/** What the Connect page's "Verify on" options hold. */
export interface VerifyOptions {
	suite: string;
	task: string;
	connectionOnly: boolean;
}

/** The body of POST /connect/verify as the Connect page sends it: empty options are left out. */
export function verifyRequestBody(agent: string, opts: VerifyOptions): ConnectVerifyRequest {
	const body: ConnectVerifyRequest = { agent };
	if (opts.connectionOnly) {
		body.skip_real_task = true;
		return body;
	}
	if (opts.suite.trim()) body.suite = opts.suite.trim();
	if (opts.suite.trim() && opts.task.trim()) body.task = opts.task.trim();
	return body;
}

/** Request path of a repository's agent file; "owner/repo" or a github.com URL. Null when it is neither. */
export function githubAgentFilePath(repo: string): string | null {
	const m = repo
		.trim()
		.replace(/^https?:\/\/github\.com\//, "")
		.replace(/\.git$/, "")
		.match(/^([A-Za-z0-9_.-]+)\/([A-Za-z0-9_.-]+)\/?$/);
	return m ? `/github/repos/${encodeURIComponent(m[1])}/${encodeURIComponent(m[2])}/agent-file` : null;
}

/** Explain a connect or run error, and check an agent file (lane L6, round 5). */
export const ufl6ConnectApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		explainConnectError: builder.mutation<ConnectExplainResponse, ConnectExplainRequest>({
			query: (body) => ({ url: "/connect/explain", method: "POST", body }),
		}),
		checkAgentFile: builder.mutation<AgentFileCheckResult, AgentFileCheckRequest>({
			query: (body) => ({ url: "/connect/agent-file/check", method: "POST", body }),
		}),
		readGithubAgentFile: builder.mutation<GitHubAgentFile, string>({
			query: (path) => path,
		}),
	}),
});

export const { useExplainConnectErrorMutation, useCheckAgentFileMutation, useReadGithubAgentFileMutation } = ufl6ConnectApi;
