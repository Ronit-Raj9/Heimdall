import "@/lib/i18n";

import { mutation, okQuery, render } from "@/lib/testing/render";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { buildQuickRunRequest, QuickRunDialog, quickRunError } from "./quickRunDialog";
import { RunButton } from "./runButton";

const api = vi.hoisted(() => ({ run: [] as unknown as readonly unknown[] }));
vi.mock("@/components/models/modelPicker", async () => await import("@/lib/testing/modelPickerStub"));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => okQuery({ mode: "live", agents: [{ id: "codex", path: "agents/codex.yaml" }] }),
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => okQuery({ mode: "live", suites: [{ id: "coding-core", version: "1.0.0", task_count: 8 }] }),
}));
vi.mock("@/lib/store/apis/quickrunApi", () => ({ useQuickRunMutation: () => api.run }));

describe("RunButton and QuickRunDialog", () => {
	beforeEach(() => {
		api.run = mutation();
	});

	it("renders an enabled Run button", () => {
		const html = render(<RunButton />);
		expect(html).toContain('data-testid="run-button"');
		expect(html).not.toContain('disabled=""');
		expect(html).toContain(">Run<");
	});

	it("asks only for what is missing", () => {
		const full = render(<QuickRunDialog onClose={() => undefined} />);
		expect(full).toContain('role="dialog"');
		expect(full).toContain('data-testid="quickrun-suite"');
		expect(full).toContain("coding-core@1.0.0 (8)");
		expect(full).toContain('data-testid="quickrun-agent"');
		expect(full).toContain('data-testid="quickrun-model"');
		const given = render(
			<QuickRunDialog
				suite="coding-core"
				agent="agents/codex.yaml"
				model={{ provider: "openai", model: "gpt-5.4-mini" }}
				onClose={() => undefined}
			/>,
		);
		expect(given).not.toContain('data-testid="quickrun-suite"');
		expect(given).not.toContain('data-testid="quickrun-agent"');
		expect(given).not.toContain('data-testid="quickrun-model"');
		expect(given).toContain('data-testid="quickrun-submit"');
	});

	it("shows the started experiment, its estimate and a link", () => {
		api.run = mutation({
			data: {
				exp_id: "e_quick_ab12",
				path: "experiments/quick-1.yaml",
				manifest_hash: "h",
				started: true,
				plan: {},
				estimate: { cost_usd: { kind: "range", low: 0.1, median: 0.2, high: 0.4 } },
			},
		});
		const html = render(<QuickRunDialog onClose={() => undefined} />);
		expect(html).toContain("Comparison e_quick_ab12 is running.");
		expect(html).toContain("$0.20 ($0.10 to $0.40)");
		expect(html).toContain("/experiments/e_quick_ab12");
	});

	it("shows the server's error", () => {
		api.run = mutation({ error: { status: 400, data: { error: { message: "suite x does not exist" } } } });
		expect(render(<QuickRunDialog onClose={() => undefined} />)).toContain("suite x does not exist");
		expect(quickRunError({ status: 503 })).toBe("HTTP 503");
	});

	it("builds the request and refuses a missing model", () => {
		const base = { suite: "", agent: "", compare: null, tasks: 5, trials: 1, budget: 5 };
		expect(buildQuickRunRequest({ ...base, model: null })).toBeNull();
		expect(buildQuickRunRequest({ ...base, model: { provider: "openai", model: " " } })).toBeNull();
		expect(buildQuickRunRequest({ ...base, suite: "coding-core", model: { provider: "openai", model: "m " } })).toEqual({
			suite: "coding-core",
			model: { provider: "openai", model: "m" },
			tasks: 5,
			trials: 1,
			budget_usd: 5,
		});
	});
});