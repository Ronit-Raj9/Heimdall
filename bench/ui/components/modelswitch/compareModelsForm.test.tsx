import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import {
	CompareModelsForm,
	compareModelsProblem,
	compareModelsSpec,
	estimateSentence,
	prefillCurrent,
	type CompareModelsInput,
} from "./compareModelsForm";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => () => {} }));
vi.mock("@/app/evaluate/views/step1Agent", () => ({ VersionPicker: () => <select data-testid="compare-models-version" /> }));
vi.mock("@/components/models/groupedModelPicker", () => ({
	GroupedModelPicker: ({ testId }: { testId: string }) => <div data-testid={testId} />,
}));
vi.mock("@/lib/store/apis/benchApi", () => ({ useGetAgentsQuery: () => ({ data: { agents: [{ id: "bot", model: "gpt-a" }] } }) }));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({
	useGetAgentCompatibilityQuery: () => ({
		data: {
			agent: "bot",
			capabilities: [],
			benchmarks: [
				{ id: "tau-retail", ok: true },
				{ id: "swe", ok: false },
			],
		},
		isLoading: false,
	}),
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
			["A", "bot", 3, "openai", "gpt-a"],
			["B", "bot", 3, "vllm", "qwen3-8b"],
		]);
		// Every label passes the server rule (evalArmIDRe in handlers/evaluations.go), fix wave 3, N211.
		const three = compareModelsSpec({ ...base, models: [...base.models, { provider: "openai", model: "gpt-c" }] });
		expect(three.arms.map((a) => a.label)).toEqual(["A", "B", "C"]);
		for (const a of three.arms) expect(a.label).toMatch(/^[A-Z][A-Za-z0-9]{0,15}$/);
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

	it("prefills the current model from the agent, only while row 0 is untouched (N211)", () => {
		const empty = [
			{ provider: "", model: "" },
			{ provider: "", model: "" },
		];
		expect(prefillCurrent(empty, "gpt-a")[0]).toEqual({ provider: "", model: "gpt-a" });
		expect(prefillCurrent([{ provider: "x", model: "m" }, empty[1]], "gpt-a")[0]).toEqual({ provider: "x", model: "m" });
		expect(prefillCurrent(empty, undefined)).toBe(empty);
	});

	it("prints one minute once, never 1 to 1 minutes (N211)", () => {
		expect(
			estimateSentence({
				runs: 4,
				est_cost_usd: { low: 0.01, high: 0.02, known: true },
				est_minutes: { low: 0.6, high: 1.4, known: true },
			}),
		).toBe("4 runs. $0.01 to $0.02. About 1 min.");
	});
});