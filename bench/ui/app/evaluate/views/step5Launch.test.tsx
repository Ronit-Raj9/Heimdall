import { mutation, render, tagOf } from "@/lib/testing/render";
import type { EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { defaultEvaluationSpec } from "./step4Setup";
import { Step5Launch, parseBudget, summaryLines } from "./step5Launch";

const q = vi.hoisted(() => ({ create: [] as unknown as readonly [unknown, Record<string, unknown>] }));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({ useCreateEvaluationMutation: () => q.create }));
vi.mock("@/lib/store/apis/modelsApi", () => ({ useGetModelsQuery: () => ({}) }));
vi.mock("@/lib/store/apis/workerConnectionsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/workerConnectionsApi")>()),
	useListWorkerConnectionsQuery: () => ({}),
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const spec = (over: Partial<EvaluationSpec> = {}): EvaluationSpec => ({
	...defaultEvaluationSpec(),
	arms: [{ label: "A", agent: "codex", version: 2, model: "gpt-4o-mini", provider: "openai" }],
	benchmarks: ["hd-harbor-tb2", "tau2"],
	...over,
});

beforeEach(() => {
	q.create = mutation();
});

describe("summaryLines", () => {
	it("describes a score evaluation in plain sentences", () => {
		expect(summaryLines(spec({ faults: ["clean", "model-down", "broken-tool"], budget_usd: 25 }))).toEqual([
			"Score codex v2 on openai/gpt-4o-mini.",
			"Run 2 benchmarks: hd-harbor-tb2, tau2.",
			"Use 8 tasks from each benchmark and try each task 2 times.",
			"Run clean, then again with: model down, broken tool.",
			"Stop when spending reaches $25.",
		]);
	});

	it("describes A/B modes and packs", () => {
		const arms = [
			{ label: "A", agent: "codex", model: "gpt-4o-mini" },
			{ label: "B", agent: "codex", model: "deepseek-chat", provider: "deepseek" },
		];
		const lines = summaryLines(spec({ mode: "models", arms, benchmarks: [], pack: "coding", trials: 1 }));
		expect(lines[0]).toBe("Compare two models: codex on gpt-4o-mini against codex on deepseek/deepseek-chat.");
		expect(lines[1]).toBe("Run the coding pack of benchmarks.");
		expect(lines[2]).toContain("try each task once");
		expect(lines[3]).toBe("Run clean only, with no faults.");
		expect(summaryLines(spec({ mode: "opponent", arms }))[0]).toContain("against the baseline");
	});
});

describe("parseBudget", () => {
	it("treats blank, zero and junk as no budget", () => {
		expect(parseBudget("")).toBeUndefined();
		expect(parseBudget("0")).toBeUndefined();
		expect(parseBudget("abc")).toBeUndefined();
		expect(parseBudget("12.5")).toBe(12.5);
	});
});

describe("Step5Launch", () => {
	it("shows the summary, budget field and an enabled Launch button", () => {
		const html = render(<Step5Launch spec={spec()} onChange={() => undefined} />);
		expect(html).toContain("Score codex v2 on openai/gpt-4o-mini.");
		expect(html).toContain('data-testid="step5-budget"');
		expect(tagOf(html, "step5-launch-button")).not.toContain('disabled=""');
		expect(html).toContain("Launch evaluation");
	});

	it("disables Launch until the spec is ready and shows server errors", () => {
		q.create = mutation({ error: { status: 400, data: { error: { message: "agent codex cannot run benchmark x: needs gpu" } } } });
		const html = render(<Step5Launch spec={spec({ benchmarks: [] })} onChange={() => undefined} />);
		expect(tagOf(html, "step5-launch-button")).toContain('disabled=""');
		expect(html).toContain("agent codex cannot run benchmark x: needs gpu");
	});
});