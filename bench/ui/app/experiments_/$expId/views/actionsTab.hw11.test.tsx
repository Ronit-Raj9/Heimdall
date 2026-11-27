import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { ActionsTab } from "./actionsTab";

const api = vi.hoisted(() => ({ adaptive: {} as unknown, verdict: {} as unknown }));
vi.mock("@/lib/store/apis/adaptiveApi", () => ({ useGetExperimentAdaptiveQuery: () => api.adaptive }));
// M4-34: the Actions tab reads one benchmark (GET /suites/{id}).
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuiteQuery: () => ({ data: undefined, isLoading: false }),
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...((await orig()) as Record<string, unknown>),
	useGetSuitesQuery: () => ({ data: { suites: [] }, isLoading: false }),
	useGetExperimentQuery: () => ({}),
	useGetVerdictQuery: () => api.verdict,
	getBenchErrorMessage: () => "failed",
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
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@tanstack/react-router", () => ({ Link: ({ children }: { children: unknown }) => children }));

// Fix wave 3, N246: a comparison whose test was broken cannot be extended, not even "anyway".
describe("Extend on a broken comparison", () => {
	it("is disabled with the reason, and offers no Extend anyway", () => {
		api.adaptive = { data: { decision: "invalid", more_evidence: null, extends: null } };
		api.verdict = { data: { verdict: { decision: "invalid", invalid_reasons: ["gate model_fidelity fired in 3 of 3 runs of arm B"] } } };
		const html = render(<ActionsTab expId="e_zz" />);
		expect(html).toContain('data-testid="actions-extend-broken"');
		expect(html).toContain(
			"This comparison&#x27;s test was broken (gate model_fidelity fired in 3 of 3 runs of arm B). Fix that first; a follow-up would be broken the same way.",
		);
		expect(html).toContain('data-testid="actions-extend-job-disabled"');
		expect(html).not.toContain('data-testid="actions-extend-force"');
	});
	it("keeps Extend anyway for a decided, valid comparison", () => {
		api.adaptive = {
			data: { decision: "improved", more_evidence: null, extends: null },
		};
		const html = render(<ActionsTab expId="e_ok" />);
		expect(html).toContain('data-testid="actions-extend-force"');
		expect(html).not.toContain('data-testid="actions-extend-broken"');
	});
});