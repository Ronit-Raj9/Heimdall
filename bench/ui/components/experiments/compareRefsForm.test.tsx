import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { compareRefsParams } from "@/lib/types/refCompare";
import { describe, expect, it, vi } from "vitest";
import { CompareRefsForm } from "./compareRefsForm";

const gh = vi.hoisted(() => ({ status: {} as unknown, repos: {} as unknown }));
vi.mock("@/lib/store/apis/benchApi", () => ({
	useGetAgentsQuery: () => ({ data: { agents: [{ id: "codex", path: "agents/codex.yaml" }] }, isSuccess: true }),
}));
vi.mock("@/lib/store/apis/githubApi", () => ({
	useGetGithubStatusQuery: () => gh.status,
	useListGithubReposQuery: () => gh.repos,
	useGetGithubRefsQuery: () => ({}),
}));
vi.mock("@/lib/store/apis/platformApi", () => ({ useGetJobQuery: () => ({}) }));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@tanstack/react-router", () => ({ Link: ({ children }: { children: unknown }) => children }));

const base = { agent: "agents/codex.yaml", suite: "coding-core@1.0.0", repo: "", refA: "main", refB: "feature/x", tasks: "", trials: 3, budget: 0 };

describe("compareRefsParams", () => {
	it("builds the job params and drops blanks", () => {
		expect(compareRefsParams(base)).toEqual({ agent: "agents/codex.yaml", suite: "coding-core@1.0.0", ref_a: "main", ref_b: "feature/x", trials: 3 });
		expect(compareRefsParams({ ...base, repo: "acme/agent", tasks: "t1, t2,t1", budget: 2 })).toMatchObject({ repo: "acme/agent", tasks: ["t1", "t2"], budget_usd: 2 });
	});
	it("waits for an agent, a suite and two different refs", () => {
		expect(compareRefsParams({ ...base, agent: "" })).toBeNull();
		expect(compareRefsParams({ ...base, refB: " main " })).toBeNull();
		expect(compareRefsParams({ ...base, refA: "" })).toBeNull();
	});
});

describe("CompareRefsForm", () => {
	it("links GitHub connect when it is not connected", () => {
		gh.status = { data: { configured: true, connected: false } };
		const html = render(<CompareRefsForm suite="coding-core@1.0.0" />);
		expect(html).toContain('data-testid="compare-refs-form-connect"');
		expect(html).toContain('data-testid="compare-refs-form-job-disabled"');
	});
	it("offers the connected account's repositories", () => {
		gh.status = { data: { configured: true, connected: true } };
		gh.repos = { data: { repos: [{ full_name: "acme/agent", default_branch: "main" }] } };
		const html = render(<CompareRefsForm />);
		expect(html).toContain('data-testid="compare-refs-form-repo"');
		expect(html).toContain("acme/agent");
	});
});
