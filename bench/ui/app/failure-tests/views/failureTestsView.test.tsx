import "@/lib/i18n";

import type { Estimate, Evaluation } from "@/lib/store/apis/evaluationsApi";
import { render } from "@/lib/testing/render";
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
	useGetSuitesQuery: () => fx.ok(fx.suites),
}));

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useListEvaluationsQuery: () => fx.ok(fx.evaluations),
	useGetAgentVersionsQuery: () => fx.ok({ agent: "codex", versions: [] }),
	useEstimateEvaluationQuery: () => fx.loading(),
	useGetEvaluationReportQuery: () => fx.loading(),
	useCreateEvaluationMutation: () => [() => undefined, { isLoading: false, isError: false }],
}));

const {
	default: FailureTestsView,
	PastTest,
	SkippedFaultsNote,
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
		expect(html).toContain("Broken tool, Misleading instruction do not apply to hd-harbor-bfcl (it has no tools) and add no runs.");
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