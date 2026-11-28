import "@/lib/i18n";

import type { Estimate } from "@/lib/store/apis/evaluationsApi";
import { render } from "@/lib/testing/render";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));

const fx = vi.hoisted(() => {
	const ok = (data: unknown) => ({
		data,
		currentData: data,
		error: undefined,
		isLoading: false,
		isFetching: false,
		isError: false,
		isSuccess: true,
		refetch: () => undefined,
	});
	return {
		ok,
		agents: { agents: [{ id: "codex", model: "gpt-4o", gateway: { provider: "openai" } }] } as unknown,
		faults: [
			{ id: "model-down", label: "Model down", help: "", layer: "model", group: "Model" },
			{ id: "broken-tool", label: "Broken tool", help: "", layer: "tool", group: "Tools" },
			{ id: "misleading-instruction", label: "Misleading instruction", help: "", layer: "instructions", group: "Instructions" },
		],
		estimate: undefined as unknown,
		specs: [] as unknown[],
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
vi.mock("@/lib/store/apis/suitesLightApi", () => ({ useGetSuitesLightQuery: () => fx.ok({ suites: [{ id: "longmemeval", description: "" }] }) }));
vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...((await importOriginal()) as Record<string, unknown>),
	useListEvaluationsQuery: () => fx.ok([]),
	useGetAgentVersionsQuery: () => fx.ok({ agent: "codex", versions: [] }),
	useEstimateEvaluationQuery: (spec: unknown) => {
		fx.specs.push(spec);
		return fx.ok(fx.estimate);
	},
	useGetBenchFaultsQuery: () => fx.ok(fx.faults),
	useGetAgentCompatibilityQuery: () => fx.ok({ benchmarks: [{ id: "longmemeval", ok: true }] }),
	useGetEvaluationReportQuery: () => fx.ok(undefined),
	useCreateEvaluationMutation: () => [() => undefined, { isLoading: false, isError: false }],
}));

const { default: FailureTestsView, failureTestSpec, formProblem, runsBreakdown, takesModelDown } = await import("./failureTestsView");
type Form = import("./failureTestsView").FailureTestForm;

const form: Form = {
	agent: "codex",
	version: 0,
	provider: "openai",
	model: "gpt-4o",
	benchmarks: ["longmemeval"],
	faults: ["model-down", "broken-tool"],
	tasks: 3,
	trials: 1,
	fallback: { provider: "openai", model: "gpt-4o-mini" },
};

// The estimate the server gives for that body (handlers uifix_h3_failover_runs_test.go):
// 3 tasks x 1 try x 3 conditions (clean, model down, primary outage) x 2 setups.
const estimate = {
	runs: 18,
	arms: 2,
	trials: 1,
	est_cost_usd: { low: 0.1, high: 0.3, known: true },
	est_minutes: { low: 2, high: 5, known: true },
	per_benchmark: [{ benchmark: "longmemeval", runs: 18, tasks: 3, conditions: ["clean", "model-outage", "primary-outage"], faults_skipped: [] }],
} as unknown as Estimate;

function withDraft(f: Form) {
	const raw = JSON.stringify(f);
	vi.stubGlobal("sessionStorage", { getItem: () => raw, setItem: () => undefined, removeItem: () => undefined });
	return render(
		<NuqsTestingAdapter searchParams="">
			<FailureTestsView />
		</NuqsTestingAdapter>,
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
	fx.specs = [];
	fx.estimate = undefined;
});

describe("fallback model on the Failure tests form", () => {
	it("is offered only when a fault takes the model or provider down", () => {
		expect(takesModelDown(["model-down"], fx.faults)).toBe(true);
		expect(takesModelDown(["broken-tool", "misleading-instruction"], fx.faults)).toBe(false);
		expect(withDraft(form)).toContain('data-testid="ft-fallback-model"');
		expect(withDraft({ ...form, faults: ["broken-tool", "misleading-instruction"] })).not.toContain('data-testid="ft-fallback"');
	});

	it("builds the wizard's failover body, and drops it without a model fault", () => {
		const spec = failureTestSpec(form, null, fx.faults);
		expect(spec.failover).toEqual({ enabled: true, fallbacks: [{ provider: "openai", model: "gpt-4o-mini" }] });
		expect(spec.arms[0]).toMatchObject({ agent: "codex", model: "gpt-4o", provider: "openai" });
		expect(spec.faults).toEqual(["clean", "model-down", "broken-tool"]);
		expect(failureTestSpec({ ...form, faults: ["broken-tool"] }, null, fx.faults).failover).toBeUndefined();
		expect(failureTestSpec({ ...form, fallback: undefined }, null, fx.faults).failover).toBeUndefined();
		// The estimate is asked for that same body.
		fx.estimate = estimate;
		withDraft(form);
		expect(fx.specs.some((s) => JSON.stringify(s) === JSON.stringify(spec))).toBe(true);
	});

	it("blocks the start on a fallback the wizard would refuse", () => {
		expect(formProblem({ ...form, fallback: { provider: "openai", model: "gpt-4o" } }, null, fx.faults)).toBe("fallback");
		expect(formProblem(form, null, fx.faults)).toBeNull();
	});
});

describe("run count from the estimate", () => {
	it("breaks the server's count down in one line", () => {
		expect(runsBreakdown(estimate)).toBe("3 tasks x 1 try x 3 conditions x 2 setups = 18 runs");
		const plain = { ...estimate, runs: 18, arms: 1, per_benchmark: [{ ...estimate.per_benchmark[0], conditions: ["a", "b", "c", "d", "e", "f"] }] };
		expect(runsBreakdown(plain as Estimate)).toBe("3 tasks x 1 try x 6 conditions = 18 runs");
		// Parts that do not multiply to the server's count: only the count.
		expect(runsBreakdown({ ...estimate, runs: 20 } as Estimate)).toBe("20 runs");
	});

	it("renders the breakdown line from the estimate", () => {
		fx.estimate = estimate;
		const html = withDraft(form);
		expect(html).toContain("3 tasks x 1 try x 3 conditions x 2 setups = 18 runs");
		expect(html).toContain("18 runs");
	});
});
