import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { CompareModelsForm, compareModelsProblem, compareModelsSpec, type CompareModelsInput } from "./compareModelsForm";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => () => {} }));
vi.mock("@/app/evaluate/views/step1Agent", () => ({ VersionPicker: () => <select data-testid="compare-models-version" /> }));
vi.mock("@/components/models/groupedModelPicker", () => ({ GroupedModelPicker: ({ testId }: { testId: string }) => <div data-testid={testId} /> }));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({
	useGetAgentCompatibilityQuery: () => ({ data: { agent: "bot", capabilities: [], benchmarks: [{ id: "tau-retail", ok: true }, { id: "swe", ok: false }] }, isLoading: false }),
	useGetEvaluationEstimateQuery: () => ({ data: undefined }),
	useCreateEvaluationMutation: () => [async () => ({}), { isLoading: false }],
}));

const base: CompareModelsInput = {
	agent: "bot",
	version: 3,
	models: [
		{ provider: "openai", model: "gpt-a" },
		{ provider: "vllm", model: "qwen3-8b" },
	],
	benchmarks: ["tau-retail"],
	tasks: 5,
	trials: 2,
	budget: 0,
};

describe("compare models form", () => {
	it("builds a model_switch spec with one arm per model on one agent version", () => {
		const s = compareModelsSpec(base);
		expect(s.mode).toBe("model_switch");
		expect(s.arms.map((a) => [a.label, a.agent, a.version, a.provider, a.model])).toEqual([
			["current", "bot", 3, "openai", "gpt-a"],
			["model-2", "bot", 3, "vllm", "qwen3-8b"],
		]);
		expect(s.budget_usd).toBeUndefined();
	});

	it("names the first problem before launch", () => {
		expect(compareModelsProblem(base)).toBeNull();
		expect(compareModelsProblem({ ...base, benchmarks: [] })).toBe("needBenchmark");
		expect(compareModelsProblem({ ...base, models: base.models.slice(0, 1) })).toBe("needModels");
		expect(compareModelsProblem({ ...base, models: [base.models[0], base.models[0]] })).toBe("duplicate");
	});

	it("lists only the benchmarks the agent can run and blocks launch until one is picked", () => {
		const html = renderToStaticMarkup(<CompareModelsForm agentId="bot" />);
		expect(html).toContain("tau-retail");
		expect(html).not.toContain(">swe<");
		expect(html).toContain('data-testid="compare-models-picker-1"');
		expect(html).toContain("Pick at least one benchmark.");
	});
});
