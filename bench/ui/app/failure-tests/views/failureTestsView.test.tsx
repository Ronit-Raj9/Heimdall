import "@/lib/i18n";

import type { Estimate, Evaluation } from "@/lib/store/apis/evaluationsApi";
import { render, tagOf } from "@/lib/testing/render";
import type { AgentSummary } from "@/lib/types/bench";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const fx = vi.hoisted(() => {
	const ok = (data: unknown) => ({
		data,
		error: undefined,
		isLoading: false,
		isFetching: false,
		isError: false,
		isSuccess: true,
		refetch: () => undefined,
	});
	const loading = () => ({
		data: undefined,
		error: undefined,
		isLoading: true,
		isFetching: true,
		isError: false,
		isSuccess: false,
		refetch: () => undefined,
	});
	return {
		ok,
		loading,
		agents: { agents: [{ id: "codex", model: "gpt-5.6-terra", gateway: { provider: "openai" } }] } as unknown,
		suites: { suites: [{ id: "hd-harbor-bfcl", description: "" }] } as unknown,
		evaluations: [] as unknown[],
		// GET /faults: the 15 catalog faults (faults/faults.go Catalog), in four groups.
		faults: [
			["model-down", "Model down", "Model"],
			["model-brownout", "Model brownout", "Model"],
			["model-slow", "Slow model", "Model"],
			["model-flaky", "Flaky model", "Model"],
			["broken-tool", "Broken tool", "Tools"],
			["tool-rate-limit", "Rate limit", "Tools"],
			["tool-slow", "Slow tool", "Tools"],
			["tool-flaky", "Flaky tool", "Tools"],
			["tool-partial", "Partial results", "Tools"],
			["tool-auth-expired", "Expired login", "Tools"],
			["tool-removed", "Tool removed", "Tools"],
			["tool-input-drift", "API version change", "Tools"],
			["tool-duplicate-effect", "Duplicate action", "Tools"],
			["misleading-instruction", "Misleading instructions", "Instructions"],
			["compound-brownout-rate-limit", "Brownout, then rate limit", "Combined"],
		].map(([id, label, group]) => ({ id, label, help: `${label} help`, layer: "model", group })),
		compat: undefined as unknown,
		estimate: undefined as unknown,
	};
});

vi.mock("@/components/models/groupedModelPicker", () => ({
	GroupedModelPicker: ({ value, testId }: { value: { model: string } | null; testId?: string }) => (
		<div data-testid={testId} data-model={value?.model ?? ""} />
	),
}));

vi.mock("@/lib/store", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useGetAgentsQuery: () => fx.ok(fx.agents),
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({ useGetSuitesLightQuery: () => fx.ok(fx.suites) }));

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useListEvaluationsQuery: () => fx.ok(fx.evaluations),
	useGetAgentVersionsQuery: () => fx.ok({ agent: "codex", versions: [] }),
	useEstimateEvaluationQuery: () => fx.estimate ?? fx.loading(),
	useGetBenchFaultsQuery: () => fx.ok(fx.faults),
	useGetAgentCompatibilityQuery: () => fx.compat ?? { data: undefined, currentData: undefined, isError: false, isLoading: false },
	useGetEvaluationReportQuery: () => fx.loading(),
	useCreateEvaluationMutation: () => [() => undefined, { isLoading: false, isError: false }],
}));

const {
	default: FailureTestsView,
	PastTest,
	SkippedFaultsNote,
	FailureTestEstimate,
	failureTestSpec,
	formProblem,
	pickAgentForm,
	resolvedModel,
	skippedFaultCount,
} = await import("./failureTestsView");
type Form = import("./failureTestsView").FailureTestForm;

const form: Form = {
	agent: "codex",
	version: 0,
	provider: "",
	model: "",
	benchmarks: ["hd-harbor-bfcl"],
	faults: ["model-down", "broken-tool"],
	tasks: 3,
	trials: 1,
};
const codex = { id: "codex", model: "gpt-5.6-terra", gateway: { provider: "openai" } } as unknown as AgentSummary;

function view(search = "") {
	return render(
		<NuqsTestingAdapter searchParams={search}>
			<FailureTestsView />
		</NuqsTestingAdapter>,
	);
}

describe("failure test budget", () => {
	it("sends budget_usd when set and leaves it to the server when empty", () => {
		expect(failureTestSpec({ ...form, model: "m", budgetText: "0.5" }).budget_usd).toBe(0.5);
		expect("budget_usd" in failureTestSpec({ ...form, model: "m", budgetText: "" })).toBe(false);
		expect("budget_usd" in failureTestSpec({ ...form, model: "m" })).toBe(false);
	});

	it("blocks launch when the budget is not a positive number", () => {
		expect(formProblem({ ...form, model: "m", budgetText: "abc" })).toBe("budget");
		expect(formProblem({ ...form, model: "m", budgetText: "-1" })).toBe("budget");
		expect(formProblem({ ...form, model: "m", budgetText: "0" })).toBe("budget");
		expect(formProblem({ ...form, model: "m", budgetText: "2" })).toBeNull();
		expect(formProblem({ ...form, model: "m", budgetText: "" })).toBeNull();
	});

	it("shows the budget field with the default note", () => {
		const html = view();
		expect(html).toContain('data-testid="ft-budget"');
		expect(html).toContain("Budget (USD)");
		expect(html).toContain("Empty: each benchmark may spend up to $5.");
	});
});

describe("failure test model", () => {
	it("keeps a chosen model when an agent is chosen after it", () => {
		const picked: Form = { ...form, agent: "", model: "gpt-6.2-sol", provider: "openai", modelTouched: true };
		const next = pickAgentForm(picked, "codex");
		expect(next.agent).toBe("codex");
		expect(next.model).toBe("gpt-6.2-sol");
		expect(resolvedModel(next, codex)).toEqual({ model: "gpt-6.2-sol", provider: "openai", fromDefault: false });
		expect(failureTestSpec(next, codex).arms[0].model).toBe("gpt-6.2-sol");
	});

	it("fills the agent's default only when no model was chosen, and says so", () => {
		const next = pickAgentForm({ ...form, agent: "" }, "codex");
		expect(resolvedModel(next, codex)).toEqual({ model: "gpt-5.6-terra", provider: "openai", fromDefault: true });
		expect(failureTestSpec(next, codex).arms[0].model).toBe("gpt-5.6-terra");
		expect(formProblem(next, codex)).toBeNull();
		const html = view("?agent=codex");
		expect(html).toContain('data-testid="ft-model-default"');
		expect(html).toContain("Model set to codex&#x27;s default: gpt-5.6-terra.");
	});
});

describe("faults that do not apply", () => {
	it("lists the estimate's skipped faults before launch", () => {
		const estimate = {
			runs: 3,
			est_cost_usd: { low: 0.01, high: 0.02, known: true },
			est_minutes: { low: 1, high: 2 },
			per_benchmark: [
				{
					benchmark: "hd-harbor-bfcl",
					faults_skipped: [
						{ fault: "broken-tool", reason: "it has no tools" },
						{ fault: "misleading-instruction", reason: "it has no tools" },
					],
				},
				{ benchmark: "other", faults_skipped: null },
			],
		} as unknown as Estimate;
		const html = render(<SkippedFaultsNote estimate={estimate} />);
		expect(html).toContain('data-testid="ft-faults-skipped"');
		expect(html).toContain("Broken tool, Misleading instruction do not apply to BFCL function calling (it has no tools) and add no runs.");
		expect(render(<SkippedFaultsNote estimate={{ ...estimate, per_benchmark: [] }} />)).toBe("");
	});

	it("counts the faults that did not apply on a past test", () => {
		const ev = {
			id: "ev_1",
			status: "running",
			created_at: "2026-11-22T00:00:00Z",
			spec: { name: "Failure test: codex", arms: [{ label: "A", agent: "codex", model: "m" }], faults: ["clean", "broken-tool"] },
			children: [
				{
					benchmark: "hd-harbor-bfcl",
					experiment_id: "e1",
					status: "running",
					faults_skipped: [
						{ fault: "broken-tool", reason: "no tools" },
						{ fault: "misleading-instruction", reason: "no tools" },
					],
				},
				{ benchmark: "b2", experiment_id: "e2", status: "running" },
			],
		} as unknown as Evaluation;
		expect(skippedFaultCount(ev)).toBe(2);
		const html = render(<PastTest ev={ev} />);
		expect(html).toContain("2 faults did not apply");
		expect(render(<PastTest ev={{ ...ev, children: [] }} />)).not.toContain("did not apply");
	});
});

describe("failure test faults from the catalog (N123)", () => {
	it("lists the 15 catalog faults in four groups with the three PS2 faults ticked", () => {
		const html = view();
		const boxes = html.match(/data-testid="ft-fault-(?!group-)[a-z-]+"/g) ?? [];
		expect(boxes).toHaveLength(15);
		for (const g of ["Model", "Tools", "Instructions", "Combined"]) expect(html).toContain(`data-testid="ft-fault-group-${g}"`);
		for (const id of ["model-down", "broken-tool", "misleading-instruction"]) expect(tagOf(html, `ft-fault-${id}`)).toContain('checked=""');
		expect(tagOf(html, "ft-fault-model-flaky")).not.toContain("checked");
		expect(html).toContain("Flaky model help");
	});
	it("sends any ticked catalog fault, clean first, without repeats", () => {
		expect(failureTestSpec({ ...form, model: "m", faults: ["model-flaky", "clean", "model-flaky", "tool-slow"] }).faults).toEqual([
			"clean",
			"model-flaky",
			"tool-slow",
		]);
	});
});

describe("failure test benchmarks (N124)", () => {
	it("hides a benchmark the chosen agent cannot run while the runnable filter is on (M5-42)", () => {
		fx.suites = {
			suites: [
				{ id: "hd-harbor-bfcl", description: "", admitted_count: 10, builtin: true, source: "library", track: "terminal" },
				{ id: "longmemeval-s", description: "", admitted_count: 20, builtin: true, source: "library", track: "memory" },
			],
		};
		const answer = {
			agent: "zz-test-loop",
			benchmarks: [{ id: "longmemeval-s", ok: false, requires: [], missing: ["sessions"], reason: "needs sessions" }],
		};
		fx.compat = { data: answer, currentData: answer, isError: false, isLoading: false };
		const html = view("?agent=codex");
		expect(html).not.toContain('data-testid="ft-benchmark-longmemeval-s"');
		expect(html).toContain('data-testid="ft-benchmark-hd-harbor-bfcl"');
		expect(html).not.toMatch(/data-testid="ft-benchmark-hd-harbor-bfcl" data-disabled/);
		expect(tagOf(html, "ft-only-runnable")).toContain('checked=""');
		fx.compat = undefined;
	});
	it("asks for an agent first when none is chosen", () => {
		expect(view()).toContain("Pick an agent to see which benchmarks it can run.");
	});
});

describe("failure test estimate (N125)", () => {
	it("says what is missing before it can show the cost", () => {
		expect(view("?agent=codex")).toContain("To see the cost: Choose at least one benchmark.");
	});
	it("shows runs, cost and minutes for a complete form, updating, the budget line and a plain line on failure", () => {
		const estimate = {
			runs: 12,
			est_cost_usd: { low: 0.4, high: 0.9, known: true },
			est_minutes: { low: 5, high: 9, known: true },
			per_benchmark: [],
			budget: {
				budget_usd: 2,
				per_benchmark_usd: 2,
				default: false,
				default_per_benchmark_usd: 5,
				suggested_usd: 2,
				max_usd: 50,
				fit: "fits",
				runs_skipped_low: 0,
				runs_skipped_high: 0,
			},
		} as unknown as Estimate;
		const html = render(
			<FailureTestEstimate problem={null} estimate={{ data: estimate, isFetching: false, isError: false }} pending={false} budgetTyped />,
		);
		expect(html).toContain("About 12 runs, $0.40 to $0.90, 5 to 9 min.");
		expect(html).toContain('data-testid="ft-budget-line"');
		expect(html).not.toContain("Updating...");
		expect(
			render(
				<FailureTestEstimate problem={null} estimate={{ data: estimate, isFetching: false, isError: false }} pending budgetTyped={false} />,
			),
		).toContain("Updating...");
		const failed = render(
			<FailureTestEstimate
				problem={null}
				estimate={{ data: undefined, isFetching: false, isError: true, error: { status: 500, data: { error: { message: "boom" } } } }}
				pending={false}
				budgetTyped={false}
			/>,
		);
		expect(failed).toContain("Could not estimate this test right now.");
		expect(failed).toMatch(/title="[^"]+"/);
	});
});