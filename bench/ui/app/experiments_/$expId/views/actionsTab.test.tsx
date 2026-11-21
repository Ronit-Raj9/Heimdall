import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { migrateParams, splitModels, followUpOf } from "@/lib/types/experimentActions";
import { describe, expect, it, vi } from "vitest";
import { ActionsTab } from "./actionsTab";

const api = vi.hoisted(() => ({
	adaptive: {} as unknown,
	suites: {} as unknown,
}));
vi.mock("@/lib/store/apis/adaptiveApi", () => ({ useGetExperimentAdaptiveQuery: () => api.adaptive }));
vi.mock("@/lib/store/apis/benchApi", () => ({
	useGetSuitesQuery: () => api.suites,
	useGetAgentsQuery: () => ({ data: { agents: [{ id: "codex", path: "agents/codex.yaml", model: "gpt-5-mini" }] }, isSuccess: true }),
}));
vi.mock("@/lib/store/apis/platformApi", () => ({ useGetJobQuery: () => ({}) }));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@tanstack/react-router", () => ({ Link: ({ children }: { children: unknown }) => children }));

describe("ActionsTab", () => {
	it("offers extend on an inconclusive verdict and lists the suite's probes", () => {
		api.adaptive = { data: { decision: "inconclusive", more_evidence: { tasks: 12, target_pp: 10, over: false }, extends: null } };
		api.suites = { data: { suites: [{ id: "coding-core", probes: [{ id: "rate-limit" }, { id: "timeout" }] }] }, isLoading: false };
		const html = render(<ActionsTab expId="e1" suite="coding-core" suiteVersion="1.0.0" />);
		expect(html).toContain('data-testid="job-run-experiment.extend"');
		expect(html).toContain("about 12 more tasks");
		expect(html).toContain('data-testid="actions-probe-rate-limit"');
		expect(html).toContain('data-testid="job-run-probe.run"');
		expect(html).toContain('value="coding-core@1.0.0"');
		// the migrate form waits for an agent and candidate models
		expect(html).toContain('data-testid="actions-migrate-form-job-disabled"');
	});
	it("refuses to extend a follow-up and explains a suite without probes", () => {
		api.adaptive = { data: { decision: "inconclusive", more_evidence: null, extends: { exp_id: "e0", kind: "trials" } } };
		api.suites = { data: { suites: [{ id: "coding-core", probes: [] }] }, isLoading: false };
		const html = render(<ActionsTab expId="e1" suite="coding-core" />);
		expect(html).toContain('data-testid="actions-extend-followup"');
		expect(html).toContain('data-testid="actions-extend-job-disabled"');
		expect(html).toContain('data-testid="actions-probe-none"');
	});
	it("needs extend anyway when the verdict is decided", () => {
		api.adaptive = { data: { decision: "improved", more_evidence: null, extends: null } };
		api.suites = { data: { suites: [] }, isLoading: false };
		const html = render(<ActionsTab expId="e1" />);
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
