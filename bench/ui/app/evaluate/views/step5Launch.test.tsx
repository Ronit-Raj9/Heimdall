import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import type { EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { EvaluateDraftProvider } from "./evaluateDraft";
import { defaultEvaluationSpec } from "./step4Setup";
import { MAX_BUDGET, NameField, Step5Launch, budgetProblem, draftNameSetter, parseBudget, summaryLines } from "./step5Launch";

const q = vi.hoisted(() => ({
	create: [] as unknown as readonly [unknown, Record<string, unknown>],
	estimate: {} as Record<string, unknown>,
	faults: {} as Record<string, unknown>,
}));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({
	useCreateEvaluationMutation: () => q.create,
	useEstimateEvaluationQuery: () => q.estimate,
	useGetBenchFaultsQuery: () => q.faults,
}));
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
	q.faults = {};
});

const CATALOG = [
	{ id: "clean", label: "Clean", help: "", layer: "none", group: "" },
	{ id: "model-brownout", label: "Model brownout", help: "", layer: "model", group: "Model" },
	{ id: "model-flaky", label: "Model flaky", help: "", layer: "model", group: "Model" },
	{ id: "broken-tool", label: "Broken tool", help: "", layer: "tool", group: "Tools" },
];

describe("summaryLines", () => {
	it("says when the Bifrost cache is the test variable", () => {
		expect(summaryLines(spec({ bifrost_cache: true }))).toContain("Also runs a second setup with Bifrost's cache on.");
		expect(summaryLines(spec()).join(" ")).not.toMatch(/cache/);
	});

	it("lists every chosen fault by its catalog name, model faults included (A2.summary)", () => {
		const lines = summaryLines(spec({ faults: ["clean", "model-brownout", "model-flaky", "broken-tool"] }), CATALOG);
		expect(lines).toContain("Run clean, then again with: model brownout, model flaky, broken tool.");
	});

	it("prints an unknown fault id as the id instead of dropping it", () => {
		expect(summaryLines(spec({ faults: ["clean", "model-brownout"] }))).toContain("Run clean, then again with: model-brownout.");
	});

	it("names the MCP route (D7.summary)", () => {
		expect(summaryLines(spec({ mcp_route: "bifrost" }))).toContain("MCP tool calls go through Bifrost's MCP gateway.");
		expect(summaryLines(spec({ mcp_route: "bifrost_code_mode" }))).toContain(
			"MCP tool calls go through Bifrost's MCP gateway, with Code Mode.",
		);
		expect(summaryLines(spec({ mcp_route: "direct" })).join(" ")).not.toMatch(/MCP/);
	});

	it("names the failover fallbacks (D6.est)", () => {
		const failover = { enabled: true, fallbacks: [{ provider: "openai", model: "gpt-6.2-sol" }, { model: "gpt-6-luna" }] };
		expect(summaryLines(spec({ failover }))).toContain(
			"Also runs a second setup with Bifrost fallbacks (openai/gpt-6.2-sol, gpt-6-luna) while the main model is down.",
		);
		expect(summaryLines(spec({ failover: { ...failover, enabled: false } })).join(" ")).not.toMatch(/fallbacks/);
	});

	it("says the release rules are checked when a gate is set", () => {
		expect(summaryLines(spec({ gate: { max_regression: 0.05 } }))).toContain("Checks the release rules when it finishes.");
		expect(summaryLines(spec()).join(" ")).not.toMatch(/release rules/);
	});

	it("uses the fault catalog in the rendered step", () => {
		q.faults = okQuery(CATALOG);
		const html = render(<Step5Launch spec={spec({ faults: ["clean", "model-brownout", "broken-tool"] })} onChange={() => undefined} />);
		expect(html).toContain("Run clean, then again with: model brownout, broken tool.");
	});

	it("says an own-model arm runs on its own model", () => {
		expect(summaryLines(spec({ arms: [{ label: "A", agent: "bot", model: "" }] }))[0]).toBe("Score bot latest version on its own model.");
	});

	it("describes a score evaluation in plain sentences", () => {
		expect(
			summaryLines(spec({ faults: ["clean", "model-down", "broken-tool"], budget_usd: 25, tasks_per_benchmark: 8, trials: 2 })),
		).toEqual([
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
		expect(lines[0]).toBe(
			"Compare two models: codex latest version on gpt-4o-mini against codex latest version on deepseek/deepseek-chat.",
		);
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
		// The "$" is a leading adornment now (M3-47), so the placeholder has none.
		expect(html).toContain('placeholder="5 per benchmark"');
		expect(html).not.toContain("No limit");
		expect(html).toContain("Empty means the default: each benchmark stops at $5.");
		expect(tagOf(html, "step5-launch-button")).not.toContain('disabled=""');
		// Launch is the shared paid button (M3-23): it names the default cap of each benchmark and asks first.
		expect(tagOf(html, "step5-launch-button")).toContain('data-paid="true"');
		expect(html).toMatch(/Launch, up to \$\d+/);
	});

	it("disables Launch until the spec is ready and shows server errors", () => {
		q.create = mutation({ error: { status: 400, data: { error: { message: "agent codex cannot run benchmark x: needs gpu" } } } });
		const html = render(<Step5Launch spec={spec({ benchmarks: [] })} onChange={() => undefined} />);
		expect(tagOf(html, "step5-launch-button")).toContain('disabled=""');
		expect(html).toContain("agent codex cannot run benchmark x: needs gpu");
	});

	it("links to Settings, Model keys, with the provider and a way back when the org has no key (M3-27)", () => {
		q.create = mutation({
			error: {
				status: 409,
				data: { error: { message: "this organization has no working openai key", type: "conflict", link: "/models" } },
			},
		});
		const html = render(<Step5Launch spec={spec()} onChange={() => undefined} />);
		expect(html).toContain("this organization has no working openai key");
		expect(tagOf(html, "step5-error-link")).toContain("/settings?tab=keys");
		expect(tagOf(html, "step5-error-link")).toContain("return=");
		expect(html).toMatch(/Add a (model key|key for )/);
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
		expect(html).toContain("With the default cap of $5.00 per benchmark, about 6 to 13 runs would not start.");
		expect(html).toContain("Set budget to $23");
	});

	it("says when the budget covers the expected cost", () => {
		q.estimate = okQuery({
			runs: 8,
			budget: budget({
				fit: "fits",
				default: false,
				budget_usd: 30,
				per_benchmark_usd: 15,
				suggested_usd: 30,
				runs_skipped_low: 0,
				runs_skipped_high: 0,
			}),
		});
		const html = render(<Step5Launch spec={spec({ budget_usd: 30 })} onChange={() => undefined} />);
		expect(html).toContain('data-testid="step5-budget-fits"');
		expect(html).not.toContain("would not start");
	});

	it("compares the typed budget with the expected cost, not the per-benchmark cap (A1.text)", () => {
		q.estimate = okQuery({
			runs: 5,
			est_cost_usd: { low: 0.06, high: 0.29, known: true },
			budget: budget({
				fit: "fits",
				default: false,
				budget_usd: 1,
				per_benchmark_usd: 5,
				suggested_usd: 1,
				runs_skipped_low: 0,
				runs_skipped_high: 0,
			}),
		});
		const html = render(<Step5Launch spec={spec({ budget_usd: 1 })} onChange={() => undefined} />);
		expect(html).toContain("Your $1.00 budget covers the expected cost ($0.06 to $0.29).");
		expect(html).not.toContain("$5 per benchmark)");
	});

	it("says Updating... while the estimate refetches", () => {
		q.estimate = { ...okQuery({ runs: 5, est_cost_usd: { low: 0.06, high: 0.29, known: true }, budget: budget({}) }), isFetching: true };
		const html = render(<Step5Launch spec={spec({ budget_usd: 1 })} onChange={() => undefined} />);
		expect(html).toContain("Updating...");
		expect(html).not.toContain("would not start");
	});
});

describe("step 5 estimate basis (D8.est)", () => {
	it("says the estimate comes from other agents' runs when from_library", () => {
		q.estimate = okQuery({ runs: 5, est_cost_usd: { low: 0.06, high: 0.29, known: true }, from_library: true });
		const html = render(<Step5Launch spec={spec()} onChange={() => undefined} />);
		expect(tagOf(html, "step5-estimate-basis")).toBeTruthy();
		expect(html).toContain("Estimated from other agents&#x27; runs of these benchmarks. Your own runs will make it more exact.");
	});

	it("says the estimate comes from this agent's runs otherwise", () => {
		q.estimate = okQuery({ runs: 5, est_cost_usd: { low: 0.01, high: 0.05, known: true } });
		const html = render(<Step5Launch spec={spec()} onChange={() => undefined} />);
		expect(html).toContain("Estimated from past runs of this agent and model.");
	});
});

describe("step 5 name (D8.name)", () => {
	it("renders a Name input with the default name as placeholder", () => {
		const html = render(
			<EvaluateDraftProvider initial={{ agent: "codex", version: 2, name: "" }}>
				<Step5Launch spec={spec({ name: "codex v2 score" })} onChange={() => undefined} />
			</EvaluateDraftProvider>,
		);
		const tag = tagOf(html, "step5-name");
		expect(tag).toContain('placeholder="codex v2 score"');
		expect(tag).toContain('maxLength="80"');
	});

	it("shows the typed name from the draft", () => {
		const html = render(
			<EvaluateDraftProvider initial={{ agent: "codex", name: "zz-test-compare" }}>
				<Step5Launch spec={spec({ name: "zz-test-compare" })} onChange={() => undefined} />
			</EvaluateDraftProvider>,
		);
		expect(tagOf(html, "step5-name")).toContain('value="zz-test-compare"');
	});

	it("sends a typed name to the setter, cut to 80 characters", () => {
		const onName = vi.fn();
		const input = findByTestId(NameField({ value: "", placeholder: "codex score", onName }), "step5-name");
		expect(input).toBeTruthy();
		const change = (input!.props as { onChange(e: { target: { value: string } }): void }).onChange;
		change({ target: { value: "my run" } });
		expect(onName).toHaveBeenCalledWith("my run");
		change({ target: { value: "x".repeat(100) } });
		expect(onName).toHaveBeenLastCalledWith("x".repeat(80));
	});

	it("wires the input to useEvaluateDraft().update({ name })", () => {
		const update = vi.fn();
		draftNameSetter({ update })("zz-test-compare");
		expect(update).toHaveBeenCalledWith({ name: "zz-test-compare" });
	});
});

function findByTestId(node: unknown, id: string): ReactElement | undefined {
	if (!node || typeof node !== "object") return undefined;
	if (Array.isArray(node)) {
		for (const n of node) {
			const hit = findByTestId(n, id);
			if (hit) return hit;
		}
		return undefined;
	}
	const el = node as ReactElement<{ children?: unknown; "data-testid"?: string }>;
	if (el.props?.["data-testid"] === id) return el;
	return findByTestId(el.props?.children, id);
}
describe("step 5 fix wave 2 (G10)", () => {
	it("checks the budget text (N91)", () => {
		expect(MAX_BUDGET).toBe(1000);
		// Fix wave 3, N190: the server's floor of $0.01 per benchmark.
		expect(budgetProblem("0.001")).toBe("The budget must be at least $0.01 per benchmark: at least $0.01 for 1 benchmark.");
		expect(budgetProblem("0.02", 3)).toBe("The budget must be at least $0.01 per benchmark: at least $0.03 for 3 benchmarks.");
		expect(budgetProblem("0.03", 3)).toBe("");
		expect(budgetProblem("")).toBe("");
		expect(budgetProblem("  ")).toBe("");
		expect(budgetProblem("2.5")).toBe("");
		expect(budgetProblem("1000")).toBe("");
		expect(budgetProblem("abc")).toBe("Enter an amount in dollars, for example 5.");
		expect(budgetProblem("-5")).toBe("Enter an amount above $0, or leave the box empty for the default of $5 per benchmark.");
		expect(budgetProblem("0")).toBe("Enter an amount above $0, or leave the box empty for the default of $5 per benchmark.");
		expect(budgetProblem("100000")).toBe("The most one evaluation can spend is $1000.");
	});

	it("blocks Launch with the sentence for a bad budget and says no default for it (N91)", () => {
		const html = render(<Step5Launch spec={spec({ budget_usd: -5 })} onChange={() => undefined} />);
		expect(tagOf(html, "step5-launch-button")).toContain('disabled=""');
		expect(html).toContain('data-testid="step5-budget-problem"');
		expect(html).toContain("Enter an amount above $0");
		expect(tagOf(html, "step5-budget")).toContain('aria-invalid="true"');
		expect(tagOf(html, "step5-budget")).toContain('type="text"');
		expect(html).not.toContain("(the default budget)");
		const high = render(<Step5Launch spec={spec({ budget_usd: 100000 })} onChange={() => undefined} />);
		expect(tagOf(high, "step5-launch-button")).toContain('disabled=""');
		expect(high).toContain("The most one evaluation can spend is $1000.");
		const blank = render(<Step5Launch spec={spec()} onChange={() => undefined} />);
		expect(blank).not.toContain("step5-budget-problem");
		expect(blank).toContain("(the default budget)");
	});

	it("warns above the organization's suggested maximum without blocking (N91)", () => {
		q.estimate = okQuery({
			runs: 4,
			budget: {
				budget_usd: 200,
				per_benchmark_usd: 100,
				default: false,
				default_per_benchmark_usd: 5,
				suggested_usd: 2,
				max_usd: 100,
				fit: "fits",
				runs_skipped_low: 0,
				runs_skipped_high: 0,
			},
		});
		const html = render(<Step5Launch spec={spec({ budget_usd: 200 })} onChange={() => undefined} />);
		expect(html).toContain("This is above the suggested maximum of $100 for your organization.");
		expect(tagOf(html, "step5-launch-button")).not.toContain('disabled=""');
	});

	it("says the real reason Launch is off (N89)", () => {
		const html = render(<Step5Launch spec={spec({ failover: { enabled: true, fallbacks: [] } })} onChange={() => undefined} />);
		expect(tagOf(html, "step5-launch-button")).toContain('disabled=""');
		expect(html).toContain("Pick a fallback model.");
		expect(html).not.toContain("Pick benchmarks and a model for every setup first.");
		expect(render(<Step5Launch spec={spec({ benchmarks: [] })} onChange={() => undefined} />)).toContain("Pick at least one benchmark.");
	});

	it("names the latest version when a setup has none (N95)", () => {
		const lines = summaryLines(
			spec({
				mode: "versions",
				arms: [
					{ label: "A", agent: "codex", model: "gpt-4o-mini", provider: "openai" },
					{ label: "B", agent: "codex", version: 1, model: "gpt-4o-mini", provider: "openai" },
				],
			}),
		);
		expect(lines[0]).toBe("Compare two versions: codex latest version on openai/gpt-4o-mini against codex v1 on openai/gpt-4o-mini.");
	});
});