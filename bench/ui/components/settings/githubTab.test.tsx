import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import type { GithubRepo } from "@/lib/types/github";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GithubTab, filterRepos, githubCallbackNotice, validBaseAgent } from "./githubTab";

const api = vi.hoisted(() => ({ q: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/githubApi", async (importOriginal) => {
	const real = await importOriginal<typeof import("@/lib/store/apis/githubApi")>();
	const { mutation: m } = await import("@/lib/testing/render");
	return {
		...real,
		useGetGithubStatusQuery: () => api.q.status,
		useListGithubReposQuery: () => api.q.repos,
		useGetGithubRefsQuery: () => api.q.refs,
		useStartGithubConnectMutation: () => m(),
		useDisconnectGithubMutation: () => m(),
		useInstallGithubCIMutation: () => m(),
	};
});

const REPO: GithubRepo = {
	full_name: "acme/widgets",
	owner: "acme",
	name: "widgets",
	private: true,
	default_branch: "main",
	html_url: "https://github.com/acme/widgets",
	can_push: true,
};

beforeEach(() => {
	api.q = {
		status: okQuery({ configured: true, connected: false }),
		repos: okQuery({ repos: [REPO], page: 1, more: false }),
		refs: okQuery({ default_branch: "main", refs: [{ name: "main", kind: "branch", sha: "abc" }] }),
	};
});

describe("GithubTab", () => {
	it("says what to set when the server has no OAuth app", () => {
		api.q.status = okQuery({ configured: false, connected: false, hint: "set HEIMDALL_GITHUB_CLIENT_ID" });
		const html = render(<GithubTab />, hostedSession("owner"));
		expect(html).toContain('data-testid="github-not-configured"');
		expect(html).toContain("HEIMDALL_GITHUB_CLIENT_ID");
		expect(html).not.toContain('data-testid="github-connect"');
	});

	it("offers Connect to an admin when not connected", () => {
		const html = render(<GithubTab />, hostedSession("admin"));
		expect(html).toContain('data-testid="github-connect"');
		expect(html).toContain('data-testid="github-not-connected"');
		expect(html).not.toContain('data-testid="github-repos"');
	});

	it("hides Connect from a member and says who can", () => {
		const html = render(<GithubTab />, hostedSession("member"));
		expect(html).not.toContain('data-testid="github-connect"');
		expect(html).toContain("Only an admin can connect");
	});

	it("lists repositories and the CI section once connected", () => {
		api.q.status = okQuery({ configured: true, connected: true, login: "octo" });
		const html = render(<GithubTab />, hostedSession("owner"));
		expect(html).toContain("Connected as octo");
		expect(html).toContain('data-testid="github-repo-acme/widgets"');
		expect(html).toContain('data-testid="github-ci-pick-first"');
		expect(html).toContain('data-testid="github-disconnect"');
	});

	it("shows the repo list error with the server message", () => {
		api.q.status = okQuery({ configured: true, connected: true, login: "octo" });
		api.q.repos = errorQuery(409, "GitHub rejected the stored token");
		const html = render(<GithubTab />, hostedSession("member"));
		expect(html).toContain('data-testid="github-repos-error"');
		expect(html).not.toContain('data-testid="github-ci-install"');
	});
});

describe("github helpers", () => {
	it("reads the callback outcome", () => {
		expect(githubCallbackNotice("?tab=github&github=connected")).toEqual({ ok: true, message: "" });
		expect(githubCallbackNotice("?github_error=nope")).toEqual({ ok: false, message: "nope" });
		expect(githubCallbackNotice("?tab=github")).toBeNull();
	});
	it("filters repos and checks base agents", () => {
		expect(filterRepos([REPO], "WIDG")).toHaveLength(1);
		expect(filterRepos([REPO], "zzz")).toHaveLength(0);
		expect(validBaseAgent("agents/a.yaml")).toBe(true);
		expect(validBaseAgent("../etc/passwd")).toBe(false);
	});
	it("keeps mutation stubs callable", () => {
		expect(mutation()[1].isLoading).toBe(false);
	});
});

describe("GitHub error copy (M8-43)", () => {
	it("drops the pointer to the page the user is on", async () => {
		const { withoutHere } = await import("./githubTab");
		expect(withoutHere("GitHub refused the token; reconnect GitHub in Settings > GitHub.")).toBe(
			"GitHub refused the token; reconnect GitHub.",
		);
		expect(withoutHere("rate limited")).toBe("rate limited");
	});
});