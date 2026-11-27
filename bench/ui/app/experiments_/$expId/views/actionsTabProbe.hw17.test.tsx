import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { ActionsTab } from "./actionsTab";

const api = vi.hoisted(() => ({ suites: {} as unknown }));
vi.mock("@/lib/store/apis/adaptiveApi", () => ({
	useGetExperimentAdaptiveQuery: () => ({ data: { decision: "score", more_evidence: null, extends: null } }),
}));
// M4-34: the Actions tab reads one benchmark (GET /suites/{id}); the list fixture answers it.
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => ({ data: undefined }),
	useGetSuiteQuery: (id: unknown) => {
		const q = api.suites as { data?: { suites?: { id: string }[] } };
		return { ...(q as object), data: q.data?.suites?.find((s) => s.id === id) };
	},
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...((await orig()) as Record<string, unknown>),
	useGetSuitesQuery: () => api.suites,
	useGetExperimentQuery: () => ({}),
	getBenchErrorMessage: () => "failed",
	useGetAgentsQuery: () => ({ data: { agents: [] }, isSuccess: true }),
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({
	...((await orig()) as Record<string, unknown>),
	useGetBenchFaultsQuery: () => ({
		data: [
			{
				id: "model-down",
				label: "Model down",
				help: "h",
				layer: "model",
				group: "Model",
			},
		],
		isLoading: false,
	}),
}));
vi.mock("@/lib/store/apis/platformApi", () => ({ useGetJobQuery: () => ({}) }));
vi.mock("@/lib/store/apis/githubApi", () => ({
	useGetGithubStatusQuery: () => ({
		data: { configured: true, connected: false },
	}),
	useListGithubReposQuery: () => ({}),
	useGetGithubRefsQuery: () => ({}),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({
	useCreateJobMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ children }: { children: unknown }) => children,
}));

describe("ProbeCard on a benchmark the server is still loading (N281)", () => {
	it("says the benchmark's own failure tests are loading instead of leaving them out", () => {
		api.suites = {
			data: { suites: [{ id: "coding-core", probes: [], partial: true }] },
			isLoading: false,
			refetch: () => undefined,
		};
		const html = render(<ActionsTab expId="e1" suite="coding-core" suiteVersion="1.0.0" />);
		expect(html).toContain('data-testid="actions-probe-own-loading"');
		expect(html).toContain("Loading this benchmark&#x27;s own failure tests...");
		expect(html).toContain('data-testid="actions-fault-model-down"');
	});
	it("shows no loading line for a full entry", () => {
		api.suites = {
			data: { suites: [{ id: "coding-core", probes: [{ id: "rate-limit" }] }] },
			isLoading: false,
			refetch: () => undefined,
		};
		const html = render(<ActionsTab expId="e1" suite="coding-core" suiteVersion="1.0.0" />);
		expect(html).not.toContain("actions-probe-own-loading");
		expect(html).toContain('data-testid="actions-probe-rate-limit"');
	});
});