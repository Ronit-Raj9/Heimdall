import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { compareRefsParams, compareRefsProblem } from "@/lib/types/refCompare";
import { describe, expect, it, vi } from "vitest";
import { CompareRefsForm, suiteChoices } from "./compareRefsForm";

const gh = vi.hoisted(() => ({ status: {} as unknown, repos: {} as unknown }));
vi.mock("@/lib/store/apis/benchApi", () => ({
	useGetAgentsQuery: () => ({ data: { agents: [{ id: "codex", path: "agents/codex.yaml" }] }, isSuccess: true }),
}));
vi.mock("@/lib/store/apis/githubApi", () => ({
	useGetGithubStatusQuery: () => gh.status,
	useListGithubReposQuery: () => gh.repos,
	useGetGithubRefsQuery: () => ({}),
}));
// X-108: the jobs drawer asks for a running count; the form tests never need it.
vi.mock("@/lib/store/apis/jobCountApi", () => ({ useCountRunningJobsQuery: () => ({ data: undefined }) }));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => ({ data: { suites: [{ id: "coding-core", version: "1.0.0" }, { id: "b" }] } }),
}));
vi.mock("@/lib/store/apis/platformApi", () => ({ useGetJobQuery: () => ({}) }));
// Round 5: the job panel's priority control has its own API file; the form tests never use it.
vi.mock("@/lib/store/apis/jobPriorityApi", () => ({ useSetJobPriorityMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@/lib/store/apis/jobCountApi", () => ({ useCountRunningJobsQuery: () => ({ data: undefined }) }));
vi.mock("@tanstack/react-router", () => ({ Link: ({ children }: { children: unknown }) => children }));

const base = {
	agent: "agents/codex.yaml",
	suite: "coding-core@1.0.0",
	repo: "",
	refA: "main",
	refB: "feature/x",
	tasks: "",
	trials: 3,
	budget: 1,
};

describe("compareRefsParams", () => {
	it("builds the job params and drops blanks", () => {
		expect(compareRefsParams(base)).toEqual({
			agent: "agents/codex.yaml",
			suite: "coding-core@1.0.0",
			ref_a: "main",
			ref_b: "feature/x",
			trials: 3,
			budget_usd: 1,
		});
		expect(compareRefsParams({ ...base, repo: "acme/agent", tasks: "t1, t2,t1", budget: 2 })).toMatchObject({
			repo: "acme/agent",
			tasks: ["t1", "t2"],
			budget_usd: 2,
		});
	});
	it("waits for an agent, a suite and two different refs", () => {
		expect(compareRefsParams({ ...base, agent: "" })).toBeNull();
		expect(compareRefsParams({ ...base, refB: " main " })).toBeNull();
		expect(compareRefsParams({ ...base, refA: "" })).toBeNull();
	});
	it("never clamps trials silently and never sends a budget of 0 (M4-93)", () => {
		expect(compareRefsProblem({ trials: 0, budget: 1 })).toBe("trials");
		expect(compareRefsProblem({ trials: 51, budget: 1 })).toBe("trials");
		expect(compareRefsProblem({ trials: 2.5, budget: 1 })).toBe("trials");
		expect(compareRefsProblem({ trials: 3, budget: 0 })).toBe("budget");
		expect(compareRefsProblem({ trials: 3, budget: 1 })).toBeNull();
		expect(compareRefsParams({ ...base, trials: 80 })).toBeNull();
		expect(compareRefsParams({ ...base, budget: 0 })).toBeNull();
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

describe("CompareRefsForm on a hosted server (N67)", () => {
	it("never offers the project's own git repository and links the GitHub tab of Settings when not connected", () => {
		gh.status = { data: { configured: true, connected: false, hosted: true } };
		const html = render(<CompareRefsForm suite="coding-core@1.0.0" />);
		expect(html).not.toContain("local server only");
		expect(html).toContain("Connect GitHub to compare two versions from your repository.");
		expect(html).toContain('data-testid="compare-refs-form-connect"');
		expect(html).toContain('data-testid="compare-refs-form-job-disabled"');
	});
	it("asks for a GitHub repository before Compare when connected", () => {
		gh.status = { data: { configured: true, connected: true, hosted: true } };
		gh.repos = { data: { repos: [{ full_name: "acme/agent", default_branch: "main" }] } };
		const html = render(<CompareRefsForm suite="coding-core@1.0.0" initialAgent="agents/codex.yaml" />);
		expect(html).not.toContain("local server only");
		expect(html).toContain("Pick a GitHub repository");
		expect(html).toContain("Pick a GitHub repository first.");
		expect(html).toContain('data-testid="compare-refs-form-job-disabled"');
	});
	it("keeps the project's own repository on a local server", () => {
		gh.status = { data: { configured: true, connected: true } };
		gh.repos = { data: { repos: [] } };
		expect(render(<CompareRefsForm />)).toContain("local server only");
	});
	it("starts from the given agent, and leaves the select empty for an agent that is not listed", () => {
		gh.status = { data: { configured: true, connected: false } };
		expect(render(<CompareRefsForm initialAgent="agents/codex.yaml" />)).toMatch(/<option value="agents\/codex.yaml" selected="">/);
		const html = render(<CompareRefsForm initialAgent="agents/gone.yaml" />);
		expect(html).not.toMatch(/<option value="agents\/codex.yaml" selected="">/);
		expect(html).not.toContain("agents/gone.yaml");
	});
});

describe("suiteChoices (M4-93)", () => {
	it("lists each benchmark ref once, sorted", () => {
		expect(suiteChoices([{ id: "z", version: "1" }, { id: "a" }, { id: "z", version: "1" }])).toEqual(["a", "z@1"]);
	});
});