import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { migrateParams, splitModels, followUpOf } from "@/lib/types/experimentActions";
import { describe, expect, it, vi } from "vitest";
import { ActionsTab } from "./actionsTab";

const api = vi.hoisted(() => ({
	adaptive: {} as unknown,
	suites: {} as unknown,
	experiment: {} as unknown,
	faults: {
		data: [
			{
				id: "model-down",
				label: "Model down",
				help: "h",
				layer: "model",
				group: "Model",
			},
			{
				id: "model-brownout",
				label: "Model brownout",
				help: "h",
				layer: "model",
				group: "Model",
			},
			{
				id: "tool-rate-limit",
				label: "Rate limit",
				help: "h",
				layer: "tool",
				group: "Tools",
			},
		],
		isLoading: false,
	} as unknown,
}));
vi.mock("@/lib/store/apis/adaptiveApi", () => ({
	useGetExperimentAdaptiveQuery: () => api.adaptive,
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
	useGetExperimentQuery: () => api.experiment,
	getBenchErrorMessage: () => "failed",
	useGetAgentsQuery: () => ({
		data: {
			agents: [{ id: "codex", path: "agents/codex.yaml", model: "gpt-5-mini" }],
		},
		isSuccess: true,
	}),
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({
	...((await orig()) as Record<string, unknown>),
	useGetBenchFaultsQuery: () => api.faults,
}));
vi.mock("@/lib/store/apis/platformApi", () => ({ useGetJobQuery: () => ({}) }));
vi.mock("@/lib/store/apis/githubApi", () => ({
	useGetGithubStatusQuery: () => ({ data: { configured: true, connected: false } }),
	useListGithubReposQuery: () => ({}),
	useGetGithubRefsQuery: () => ({}),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@tanstack/react-router", () => ({ Link: ({ children }: { children: unknown }) => children }));

describe("ActionsTab", () => {
	it("offers extend on an inconclusive verdict and lists the suite's probes", () => {
		api.adaptive = { data: { decision: "inconclusive", more_evidence: { tasks: 12, target_pp: 10, over: false }, extends: null } };
		api.suites = { data: { suites: [{ id: "coding-core", probes: [{ id: "rate-limit" }, { id: "timeout" }] }] }, isLoading: false };
		const html = render(<ActionsTab openAll expId="e1" suite="coding-core" suiteVersion="1.0.0" />);
		expect(html).toContain('data-testid="job-run-experiment.extend"');
		expect(html).toContain("about 12 more tasks");
		expect(html).toContain('data-testid="actions-probe-rate-limit"');
		// N65: nothing ticked no longer means every probe; Run waits for a tick.
		expect(html).toContain('data-testid="actions-probe-job-disabled"');
		expect(html).toContain('value="coding-core@1.0.0"');
		// the migrate form waits for an agent and candidate models
		expect(html).toContain('data-testid="actions-migrate-form-job-disabled"');
		// the compare-refs card waits for refs and links GitHub connect when it is not connected
		expect(html).toContain('data-testid="actions-compare-refs-form-job-disabled"');
		expect(html).toContain('data-testid="actions-compare-refs-form-connect"');
	});
	it("refuses to extend a follow-up and offers the fault catalog on a suite without probes", () => {
		api.adaptive = { data: { decision: "inconclusive", more_evidence: null, extends: { exp_id: "e0", kind: "trials" } } };
		api.suites = { data: { suites: [{ id: "coding-core", probes: [] }] }, isLoading: false };
		const html = render(<ActionsTab openAll expId="e1" suite="coding-core" />);
		expect(html).toContain('data-testid="actions-extend-followup"');
		expect(html).toContain('data-testid="actions-extend-job-disabled"');
		expect(html).not.toContain('data-testid="actions-probe-none"');
		expect(html).toContain('data-testid="actions-fault-model-brownout"');
		expect(html).not.toContain('data-testid="actions-probe-own"');
	});
	it("needs extend anyway when the verdict is decided", () => {
		api.adaptive = { data: { decision: "improved", more_evidence: null, extends: null } };
		api.suites = { data: { suites: [] }, isLoading: false };
		const html = render(<ActionsTab openAll expId="e1" />);
		expect(html).toContain('data-testid="actions-extend-force"');
		expect(html).toContain('data-testid="actions-extend-job-disabled"');
	});
});

describe("experiment action params", () => {
	it("builds migrate params only when complete", () => {
		expect(splitModels(" a, b ,a,, ")).toEqual(["a", "b"]);
		expect(migrateParams({ agent: "", suite: "s@1", from: "a", to: "b", trials: 3, budget: 0 })).toBeNull();
		expect(migrateParams({ agent: "agents/x.yaml", suite: "s@1", from: "a", to: "a", trials: 3, budget: 0 })).toBeNull();
		expect(migrateParams({ agent: "agents/x.yaml", suite: " s@1 ", from: "a", to: "b, c", trials: 0, budget: 2 })).toEqual({
			agent: "agents/x.yaml",
			suite: "s@1",
			from: "a",
			to: ["b", "c"],
			trials: 1,
			budget_usd: 2,
		});
		expect(followUpOf({ exp_id: "e2", started: true })?.exp_id).toBe("e2");
		expect(followUpOf({ ok: true })).toBeNull();
	});
});

describe("ActionsTab fault test (N65)", () => {
	it("lists the fault catalog grouped, the benchmark's own probes apart, and waits for a tick", () => {
		api.adaptive = { data: { decision: "improved", more_evidence: null, extends: null } };
		api.suites = { data: { suites: [{ id: "coding-core", probes: [{ id: "rate-limit" }] }] }, isLoading: false };
		api.experiment = {};
		const html = render(<ActionsTab openAll expId="e1" suite="coding-core" />);
		expect(html).toContain("Faults to add");
		expect(html).toContain('data-testid="actions-probe-group-Model"');
		expect(html).toContain('data-testid="actions-probe-group-Tools"');
		expect(html).toContain('data-testid="actions-fault-model-down"');
		expect(html).toContain("This benchmark&#x27;s own failure tests");
		expect(html).toContain('data-testid="actions-probe-rate-limit"');
		expect(html).toContain('data-testid="actions-probe-job-disabled"');
		expect(html).toContain("Tick at least one fault.");
		expect(html).toContain("Re-run each setup with faults on the same tasks.");
	});
});

describe("ActionsTab setups and Extend words (N66)", () => {
	it("prefills Compare models and Compare two git refs with setup A's agent and model", () => {
		api.adaptive = { data: { decision: "improved", more_evidence: null, extends: null } };
		api.suites = { data: { suites: [] }, isLoading: false };
		api.experiment = {
			data: {
				setups: [
					{ arm: "B", agent: "claude", agent_file: "agents/claude.yaml", model: "other" },
					{ arm: "A", agent: "codex", agent_file: "agents/codex.yaml", model: "gpt-6-luna" },
				],
			},
		};
		const html = render(<ActionsTab openAll expId="e1" suite="coding-core" />);
		expect(html).toMatch(/data-testid="actions-migrate-form-agent"[\s\S]*?<option value="agents\/codex.yaml" selected="">/);
		expect(html).toMatch(
			/value="gpt-6-luna"[^>]*data-testid="actions-migrate-form-from"|data-testid="actions-migrate-form-from"[^>]*value="gpt-6-luna"/,
		);
		expect(html).toMatch(/data-testid="actions-compare-refs-form-agent"[\s\S]*?<option value="agents\/codex.yaml" selected="">/);
	});
	it("reads a score run in plain words and needs no Extend anyway", () => {
		api.adaptive = { data: { decision: "score", more_evidence: null, extends: null } };
		api.suites = { data: { suites: [] }, isLoading: false };
		api.experiment = {};
		const html = render(<ActionsTab openAll expId="e1" />);
		expect(html).toContain("Current result: score only (one setup)");
		expect(html).not.toContain('data-testid="actions-extend-force"');
		expect(html).toContain('data-testid="job-run-experiment.extend"');
	});
	it("names a decided verdict in plain words", () => {
		api.adaptive = {
			data: { decision: "regressed", more_evidence: null, extends: null },
		};
		api.experiment = {};
		expect(render(<ActionsTab openAll expId="e1" />)).toContain("Current result: B is worse");
	});
});