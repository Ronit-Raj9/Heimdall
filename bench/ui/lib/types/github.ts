// GitHub repository connection (lane L07, handlers/github.go).

export interface GithubStatus {
	configured: boolean;
	connected: boolean;
	login?: string;
	connected_by?: string;
	connected_at?: string;
	hint?: string;
	/** A hosted server: a project has no git history of its own, so refs come from a GitHub repository (fix wave 2, N67). */
	hosted?: boolean;
}

export interface GithubRepo {
	full_name: string;
	owner: string;
	name: string;
	private: boolean;
	default_branch: string;
	html_url: string;
	description?: string;
	updated_at?: string;
	can_push: boolean;
}

export interface GithubReposResponse {
	repos: GithubRepo[];
	page: number;
	more: boolean;
}

export interface GithubRef {
	name: string;
	kind: "branch" | "tag";
	sha: string;
}

export interface GithubRefsResponse {
	default_branch: string;
	refs: GithubRef[];
}

export type GithubCIMode = "recorded" | "live";

export interface GithubCIInstallRequest {
	owner: string;
	repo: string;
	base?: string;
	mode?: GithubCIMode;
	base_agent?: string;
	force?: boolean;
}

export interface GithubCIInstallResult {
	number: number;
	url: string;
	branch: string;
	base: string;
	path: string;
}

// Types the "github" i18n namespace from its catalog (lib/i18n/resources.ts LaneResources).
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		github: typeof import("@/locales/en/github.json");
	}
}
