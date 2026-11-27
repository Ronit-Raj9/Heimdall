import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { ActionsTab } from "./actionsTab";

const sentence =
	"e_zz's test was broken (gate model_fidelity fired in 3 of 3 runs of arm B). Fix that first; a follow-up would be broken the same way.";

vi.mock("@/lib/store/apis/adaptiveApi", () => ({
	useGetExperimentAdaptiveQuery: () => ({ data: { decision: "inconclusive", more_evidence: null, extends: null } }),
}));
// M4-34: the Actions tab reads one benchmark (GET /suites/{id}).
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => ({ data: undefined }),
	useGetSuiteQuery: () => ({ data: undefined, isLoading: false }),
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...((await orig()) as Record<string, unknown>),
	useGetSuitesQuery: () => ({ data: { suites: [] }, isLoading: false }),
	useGetExperimentQuery: () => ({}),
	useGetVerdictQuery: () => ({}),
	useGetAgentsQuery: () => ({ data: { agents: [] }, isSuccess: true }),
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({
	...((await orig()) as Record<string, unknown>),
	useGetBenchFaultsQuery: () => ({ data: [], isLoading: false }),
}));
vi.mock("@/lib/store/apis/platformApi", () => ({ useGetJobQuery: () => ({}) }));
vi.mock("@/lib/store/apis/githubApi", () => ({
	useGetGithubStatusQuery: () => ({ data: { configured: true, connected: false } }),
	useListGithubReposQuery: () => ({}),
	useGetGithubRefsQuery: () => ({}),
}));
// The server's 400 answer to an extend the job refuses: the job create prefix, then the plain sentence.
vi.mock("@/lib/store/apis/jobRunApi", () => ({
	useCreateJobMutation: () => [
		vi.fn(),
		{
			isLoading: false,
			error: { status: 400, data: { error: { message: `create job: invalid job parameters: ${sentence}` } } },
			reset: vi.fn(),
		},
	],
}));
vi.mock("@tanstack/react-router", () => ({ Link: ({ children }: { children: unknown }) => children }));

// Fix wave 3 (H11 extend refusal with H15's plainJobError): the Extend card shows the refusal sentence
// without the server's "invalid job parameters" lead.
describe("Extend refused by the server", () => {
	it("shows only the plain refusal sentence", () => {
		const html = render(<ActionsTab expId="e_zz" />);
		const at = html.indexOf('data-testid="job-run-experiment.extend-error"');
		expect(at).toBeGreaterThan(-1);
		// The visible text of the error line (its title keeps the full server message for support).
		const shown = html.slice(html.indexOf(">", at) + 1, html.indexOf("</p>", at));
		expect(shown).toContain("e_zz&#x27;s test was broken (gate model_fidelity fired in 3 of 3 runs of arm B).");
		expect(shown).not.toContain("invalid job parameters");
		expect(shown).not.toContain("create job:");
	});
});