import type { ConnectFailure } from "@/lib/types/connect";

/** POST /connect/explain (bench/handlers/connect.go ConnectExplainRequest). */
export interface ConnectExplainRequest {
	text: string;
}

/** The answer of POST /connect/explain: the catalog entry the error maps to, when one does. */
export interface ConnectExplainResponse {
	found: boolean;
	failure?: ConnectFailure;
}

/** POST /connect/agent-file/check (bench/handlers/connecttraces.go AgentFileCheckRequest). */
export interface AgentFileCheckRequest {
	yaml: string;
}

/** The answer of POST /connect/agent-file/check: valid or not, and the agent config it becomes. */
export interface AgentFileCheckResult {
	ok: boolean;
	error?: string;
	config_yaml?: string;
}

/** GET /github/repos/{owner}/{repo}/agent-file: the repository's .heimdall/agent.yaml, checked. */
export interface GitHubAgentFile extends AgentFileCheckResult {
	found: boolean;
	path: string;
	ref?: string;
	yaml?: string;
}