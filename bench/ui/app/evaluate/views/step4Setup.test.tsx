import { okQuery, render, tagOf } from "@/lib/testing/render";
import type { EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import type { ModelKey } from "@/lib/types/models";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { groupModels, keyStateFor, type CatalogProvider } from "@/components/models/groupedModelPicker";
import { Step4Setup, defaultEvaluationSpec, estimateText, specReady, toggleFault } from "./step4Setup";

const q = vi.hoisted(() => ({
	models: {} as Record<string, unknown>,
	estimate: {} as Record<string, unknown>,
	estimateArgs: [] as unknown[],
}));
vi.mock("@/lib/store/apis/modelsApi", () => ({ useGetModelsQuery: () => q.models }));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({
	useEstimateEvaluationQuery: (spec: unknown, opts: unknown) => {
		q.estimateArgs.push([spec, opts]);
		return q.estimate;
	},
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const catalog: CatalogProvider[] = [
	{ id: "openai", name: "OpenAI", needs_base_url: false, key_hint: "", models: [{ id: "gpt-4o-mini", purposes: ["agent"], rank: 1 }] },
	{
		id: "deepseek",
		name: "DeepSeek",
		needs_base_url: false,
		key_hint: "",
		models: [
			{ id: "deepseek-chat", purposes: ["agent"], rank: 1, open_weights: true, family: "deepseek" },
			{ id: "deepseek-reasoner", purposes: ["agent"], rank: 2, open_weights: true, family: "deepseek" },
		],
	},
	{
		id: "mistral",
		name: "Mistral",
		needs_base_url: false,
		key_hint: "",
		models: [{ id: "devstral-medium-latest", purposes: ["agent"], rank: 1, family: "mistral" }],
	},
];

const key = (over: Partial<Omit<ModelKey, "provider">> & { provider?: string }): ModelKey =>
	({
		id: "k1",
		provider: "openai",
		label: "",
		last4: "",
		status: "ok",
		models: [],
		budget_usd: 0,
		month_spent_usd: 0,
		over_budget: false,
		created_at: "",
		updated_at: "",
		...over,
	}) as ModelKey;

const spec = (over: Partial<EvaluationSpec> = {}): EvaluationSpec => ({
	...defaultEvaluationSpec(),
	arms: [{ label: "A", agent: "codex", version: 2, model: "deepseek-chat", provider: "deepseek" }],
	benchmarks: ["hd-harbor-tb2"],
	...over,
});

beforeEach(() => {
	q.models = okQuery({ providers: catalog, keys: [key({}), key({ id: "k2", provider: "mistral", status: "invalid" })], keys_ready: true });
	q.estimate = okQuery({ runs: 20, est_cost_usd: { low: 1.2, high: 3.4, known: true }, est_minutes: { low: 10, high: 25 } });
	q.estimateArgs = [];
});

describe("groupModels", () => {
	it("lists every provider, keyed ones first, and tags open-weight models", () => {
		const keys = [key({ provider: "deepseek" })];
		const groups = groupModels(catalog, keys);
		expect(groups.map((g) => g.provider)).toEqual(["deepseek", "openai", "mistral"]);
		expect(groups[0].models).toEqual([
			{ id: "deepseek-chat", open: true, family: "deepseek" },
			{ id: "deepseek-reasoner", open: true, family: "deepseek" },
		]);
		expect(groups[1].keyState).toBe("none");
	});

	it("filters by model id, family, provider name and the word open", () => {
		expect(groupModels(catalog, [], "reasoner").flatMap((g) => g.models.map((m) => m.id))).toEqual(["deepseek-reasoner"]);
		expect(groupModels(catalog, [], "open").map((g) => g.provider)).toEqual(["openai", "deepseek"]);
		expect(groupModels(catalog, [], "mistral").map((g) => g.provider)).toEqual(["mistral"]);
		expect(groupModels(catalog, [], "openai")[0].models.map((m) => m.id)).toEqual(["gpt-4o-mini"]);
	});

	it("knows when a provider has no working key", () => {
		const keys = [key({ provider: "mistral", status: "invalid" })];
		expect(keyStateFor("mistral", keys)).toBe("invalid");
		expect(keyStateFor("openai", keys)).toBe("none");
		expect(keyStateFor("mistral", [...keys, key({ id: "k3", provider: "mistral" })])).toBe("ok");
	});
});

describe("step 4 helpers", () => {
	it("keeps clean first and always on", () => {
		expect(toggleFault(["clean"], "broken-tool", true)).toEqual(["clean", "broken-tool"]);
		expect(toggleFault(["broken-tool", "model-down"], "broken-tool", false)).toEqual(["clean", "model-down"]);
		expect(toggleFault(["clean", "model-down"], "model-down", true)).toEqual(["clean", "model-down"]);
	});

	it("needs benchmarks and a model on every arm", () => {
		expect(specReady(spec())).toBe(true);
		expect(specReady(spec({ benchmarks: [] }))).toBe(false);
		expect(specReady(spec({ benchmarks: [], pack: "coding" }))).toBe(true);
		expect(specReady(spec({ arms: [{ label: "A", agent: "x", model: "" }] }))).toBe(false);
	});

	it("words the estimate, with unknown prices said plainly", () => {
		expect(estimateText({ runs: 1, est_cost_usd: { low: 2, high: 2, known: true }, est_minutes: { low: 4, high: 4 } })).toEqual({
			runs: "1 run",
			cost: "$2.00",
			minutes: "4 min",
		});
		expect(estimateText({ runs: 40, est_cost_usd: { low: 0, high: 0, known: false }, est_minutes: { low: 10.4, high: 30 } }).cost).toBe(
			"price not known",
		);
		expect(estimateText({ runs: 40, est_cost_usd: { low: 12, high: 40.6, known: true }, est_minutes: { low: 1, high: 2 } }).cost).toBe(
			"$12 to $41",
		);
	});
});

describe("Step4Setup", () => {
	it("renders the model picker, choices, faults and the live estimate", () => {
		const html = render(<Step4Setup spec={spec({ faults: ["clean", "broken-tool"] })} onChange={() => undefined} />);
		expect(html).toContain("deepseek-chat  [open]");
		expect(html).toContain('data-testid="step4-model-A-open"');
		expect(tagOf(html, "step4-tasks-8")).toContain('aria-checked="true"');
		expect(tagOf(html, "step4-trials-2")).toContain('aria-checked="true"');
		expect(html).toMatch(/data-testid="step4-fault-broken-tool"><input type="checkbox" class="mt-1" checked=""/);
		expect(html).not.toMatch(/data-testid="step4-fault-model-down"><input type="checkbox" class="mt-1" checked=""/);
		expect(html).toContain("Some tool calls return errors");
		expect(html).toContain("20 runs");
		expect(html).toContain("$1.20 to $3.40");
		expect(html).toContain("10 to 25 min");
		expect(q.estimateArgs[0]).toEqual([expect.objectContaining({ benchmarks: ["hd-harbor-tb2"] }), { skip: false }]);
	});

	it("warns with a keys link when the chosen provider has no working key", () => {
		const html = render(
			<Step4Setup
				spec={spec({ arms: [{ label: "A", agent: "codex", model: "devstral-medium-latest", provider: "mistral" }] })}
				onChange={() => undefined}
			/>,
		);
		expect(html).toContain('data-testid="step4-model-A-no-key"');
		expect(html).toContain("The Mistral key does not work.");
		expect(html).toContain('href="/models?add=mistral"');
		expect(html).toContain("Mistral (key not working)");
	});

	it("shows one picker per arm and skips the estimate until ready", () => {
		const two = spec({
			mode: "models",
			benchmarks: [],
			arms: [
				{ label: "A", agent: "codex", model: "gpt-4o-mini", provider: "openai" },
				{ label: "B", agent: "codex", model: "", provider: "" },
			],
		});
		const html = render(<Step4Setup spec={two} onChange={() => undefined} />);
		expect(html).toContain('data-testid="step4-arm-A"');
		expect(html).toContain('data-testid="step4-arm-B"');
		expect(html).toContain("Pick benchmarks and a model");
		expect(q.estimateArgs[0]).toEqual([two, { skip: true }]);
	});

	it("says price not known when the estimate has no price", () => {
		q.estimate = okQuery({ runs: 6, est_cost_usd: { low: 0, high: 0, known: false }, est_minutes: { low: 3, high: 9 } });
		expect(render(<Step4Setup spec={spec()} onChange={() => undefined} />)).toContain("price not known");
	});
});