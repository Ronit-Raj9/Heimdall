import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import type { EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { defaultEvaluationSpec } from "./step4Setup";
import { Step5Launch, parseBudget, summaryLines } from "./step5Launch";

const q = vi.hoisted(() => ({ create: [] as unknown as readonly [unknown, Record<string, unknown>], estimate: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({ useCreateEvaluationMutation: () => q.create, useEstimateEvaluationQuery: () => q.estimate }));
vi.mock("@/lib/store/apis/modelsApi", () => ({ useGetModelsQuery: () => ({}) }));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => ({ data: { agents: [{ id: "bot", owns_model: true }, { id: "codex" }] } }),
}));
vi.mock("@/lib/store/apis/workerTargetApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/workerTargetApi")>()),
	useGetWorkerTargetQuery: () => ({}),
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
	q.estimate = {};
});

describe("summaryLines", () => {
	it("says an own-model arm runs on its own model", () => {
		expect(summaryLines(spec({ arms: [{ label: "A", agent: "bot", model: "" }] }))[0]).toBe("Score bot on its own model.");
	});

	it("describes a score evaluation in plain sentences", () => {
		expect(summaryLines(spec({ faults: ["clean", "model-down", "broken-tool"], budget_usd: 25, tasks_per_benchmark: 8, trials: 2 }))).toEqual([
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
		expect(lines[4]).toBe("Stop each benchmark when its spending reaches $5 (the default budget).");
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
		expect(html).toContain('placeholder="$5 per benchmark"');
		expect(html).not.toContain("No limit");
		expect(html).toContain("Empty means the default: each benchmark stops at $5.");
		expect(tagOf(html, "step5-launch-button")).not.toContain('disabled=""');
		expect(html).toContain("Launch evaluation");
	});

	it("disables Launch until the spec is ready and shows server errors", () => {
		q.create = mutation({ error: { status: 400, data: { error: { message: "agent codex cannot run benchmark x: needs gpu" } } } });
		const html = render(<Step5Launch spec={spec({ benchmarks: [] })} onChange={() => undefined} />);
		expect(tagOf(html, "step5-launch-button")).toContain('disabled=""');
		expect(html).toContain("agent codex cannot run benchmark x: needs gpu");
	});

	it("links to Models & keys when the org has no key for the model's provider", () => {
		q.create = mutation({
			error: { status: 409, data: { error: { message: "this organization has no working openai key", type: "conflict", link: "/models" } } },
		});
		const html = render(<Step5Launch spec={spec()} onChange={() => undefined} />);
		expect(html).toContain("this organization has no working openai key");
		expect(tagOf(html, "step5-error-link")).toContain('href="/models"');
		expect(html).toContain("Add a model key");
	});
});

describe("Step5Launch budget check", () => {
	const budget = (over: Record<string, unknown>) => ({
		budget_usd: 10,
		per_benchmark_usd: 5,
		default: true,
		default_per_benchmark_usd: 5,
		suggested_usd: 23,
		max_usd: 100,
		fit: "exceeds",
		runs_skipped_low: 6,
		runs_skipped_high: 13,
		...over,
	});

	it("warns how many runs the default cap leaves unstarted and offers the suggested budget", () => {
		q.estimate = okQuery({ runs: 32, budget: budget({}) });
		const html = render(<Step5Launch spec={spec()} onChange={() => undefined} />);
		expect(html).toContain("With the default cap of $5 per benchmark, about 6 to 13 runs would not start.");
		expect(html).toContain("Set budget to $23");
	});

	it("says when the budget covers the expected cost", () => {
		q.estimate = okQuery({ runs: 8, budget: budget({ fit: "fits", default: false, budget_usd: 30, per_benchmark_usd: 15, suggested_usd: 30, runs_skipped_low: 0, runs_skipped_high: 0 }) });
		const html = render(<Step5Launch spec={spec({ budget_usd: 30 })} onChange={() => undefined} />);
		expect(html).toContain('data-testid="step5-budget-fits"');
		expect(html).not.toContain("would not start");
	});
});
