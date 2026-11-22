import "@/lib/i18n";

import { okQuery, render, tagOf } from "@/lib/testing/render";
import type { Estimate, EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import type { ModelKey } from "@/lib/types/models";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { groupModels, keyStateFor, modelOptionLabel, providerOfModel, type CatalogProvider } from "@/components/models/groupedModelPicker";
import {
	Step4Setup,
	concurrencyNote,
	defaultEvaluationSpec,
	estimateFailed,
	estimateNotes,
	estimateText,
	groupFaults,
	powerText,
	specProblem,
	specReady,
	faultsNotApplying,
	nothingRunsReason,
	presetsRequest,
	toggleFault,
	withMCPRoute,
} from "./step4Setup";
import { EMPTY_DRAFT, applyPatch, draftToSpec, specToPatch } from "./evaluateDraft";
import { createDebouncer } from "./useDebounced";

const q = vi.hoisted(() => ({
	models: {} as Record<string, unknown>,
	estimate: {} as Record<string, unknown>,
	estimateArgs: [] as unknown[],
	faults: {} as Record<string, unknown>,
}));
vi.mock("@/lib/store/apis/modelsApi", () => ({
	useGetModelsQuery: () => q.models,
}));
vi.mock("@/lib/store/apis/evaluationsApi", () => ({
	useEstimateEvaluationQuery: (spec: unknown, opts: unknown) => {
		q.estimateArgs.push([spec, opts]);
		return q.estimate;
	},
	useGetBenchFaultsQuery: () => q.faults,
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => ({ data: { agents: [{ id: "bot", owns_model: true }, { id: "codex" }] } }),
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const catalog: CatalogProvider[] = [
	{
		id: "openai",
		name: "OpenAI",
		needs_base_url: false,
		key_hint: "",
		models: [{ id: "gpt-4o-mini", purposes: ["agent"], rank: 1 }],
	},
	{
		id: "deepseek",
		name: "DeepSeek",
		needs_base_url: false,
		key_hint: "",
		models: [
			{
				id: "deepseek-chat",
				purposes: ["agent"],
				rank: 1,
				open_weights: true,
				family: "deepseek",
			},
			{
				id: "deepseek-reasoner",
				purposes: ["agent"],
				rank: 2,
				open_weights: true,
				family: "deepseek",
			},
		],
	},
	{
		id: "mistral",
		name: "Mistral",
		needs_base_url: false,
		key_hint: "",
		models: [
			{
				id: "devstral-medium-latest",
				purposes: ["agent"],
				rank: 1,
				family: "mistral",
			},
		],
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
	arms: [
		{
			label: "A",
			agent: "codex",
			version: 2,
			model: "deepseek-chat",
			provider: "deepseek",
		},
	],
	benchmarks: ["hd-harbor-tb2"],
	...over,
});

beforeEach(() => {
	q.models = okQuery({
		providers: catalog,
		keys: [key({}), key({ id: "k2", provider: "mistral", status: "invalid" })],
		keys_ready: true,
	});
	q.estimate = okQuery({
		runs: 20,
		est_cost_usd: { low: 1.2, high: 3.4, known: true },
		est_minutes: { low: 10, high: 25 },
	});
	q.estimateArgs = [];
	q.faults = { data: undefined, isLoading: true, isError: false };
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
		expect(
			estimateText({
				runs: 1,
				est_cost_usd: { low: 2, high: 2, known: true },
				est_minutes: { low: 4, high: 4 },
			}),
		).toEqual({
			runs: "1 run",
			cost: "$2.00",
			minutes: "4 min",
		});
		expect(estimateText({ runs: 40, est_cost_usd: { low: 0, high: 0, known: false }, est_minutes: { low: 10.4, high: 30 } }).cost).toBe(
			"price not known",
		);
		expect(
			estimateText({
				runs: 40,
				est_cost_usd: { low: 0, high: 0, known: false, reason: "no price for gpt-x" },
				est_minutes: { low: 1, high: 2 },
			}).cost,
		).toBe("price not known (no price for gpt-x)");
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
		expect(tagOf(html, "step4-tasks-10")).toContain('aria-checked="true"');
		expect(tagOf(html, "step4-trials-1")).toContain('aria-checked="true"');
		expect(tagOf(html, "step4-concurrency-4")).toContain('aria-checked="true"');
		expect(html).toContain('data-testid="step4-tasks-50"');
		expect(html).toMatch(/data-testid="step4-fault-broken-tool"><input type="checkbox" class="mt-1" checked=""/);
		expect(html).not.toMatch(/data-testid="step4-fault-model-down"><input type="checkbox" class="mt-1" checked=""/);
		expect(html).toContain("Some tool calls return errors");
		expect(html).toContain("20 runs");
		expect(html).toContain("$1.20 to $3.40");
		expect(html).toContain("10 to 25 min");
		expect(q.estimateArgs[0]).toEqual([expect.objectContaining({ benchmarks: ["hd-harbor-tb2"] }), { skip: false }]);
		expect((q.estimateArgs[0] as [Record<string, unknown>])[0]).not.toHaveProperty("presets");
		expect(q.estimateArgs[1]).toEqual([expect.objectContaining({ benchmarks: ["hd-harbor-tb2"], presets: true }), { skip: false }]);
	});

	it("warns with a keys link when the chosen provider has no working key", () => {
		const html = render(
			<Step4Setup
				spec={spec({
					arms: [
						{
							label: "A",
							agent: "codex",
							model: "devstral-medium-latest",
							provider: "mistral",
						},
					],
				})}
				onChange={() => undefined}
			/>,
		);
		expect(html).toContain('data-testid="step4-model-A-no-key"');
		expect(html).toContain("The Mistral key does not work.");
		expect(html).toContain('href="/models?add=mistral"');
		expect(html).toContain("Mistral (key not working)");
	});

	it("lets an agent that brings its own model leave the model blank", () => {
		const own = spec({ arms: [{ label: "A", agent: "bot", model: "" }] });
		expect(specReady(own)).toBe(false);
		expect(specReady(own, new Set(["bot"]))).toBe(true);
		const html = render(<Step4Setup spec={own} onChange={() => undefined} />);
		expect(html).toContain('data-testid="step4-own-model-A"');
		expect(html).toContain("Its own model.");
		expect(html).not.toContain('data-testid="step4-model-A"');
		expect(q.estimateArgs[0]).toEqual([own, { skip: false }]);
		expect(q.estimateArgs[1]).toEqual([presetsRequest(own), { skip: false }]);
	});

	it("shows one picker per arm and skips the estimate until ready", () => {
		const two = spec({
			mode: "models",
			benchmarks: [],
			arms: [
				{
					label: "A",
					agent: "codex",
					model: "gpt-4o-mini",
					provider: "openai",
				},
				{ label: "B", agent: "codex", model: "", provider: "" },
			],
		});
		const html = render(<Step4Setup spec={two} onChange={() => undefined} />);
		expect(html).toContain('data-testid="step4-arm-A"');
		expect(html).toContain('data-testid="step4-arm-B"');
		expect(html).toContain("Pick at least one benchmark.");
		expect(q.estimateArgs[0]).toEqual([two, { skip: true }]);
		expect(q.estimateArgs[1]).toEqual([presetsRequest(two), { skip: true }]);
	});

	it("says price not known when the estimate has no price", () => {
		q.estimate = okQuery({
			runs: 6,
			est_cost_usd: { low: 0, high: 0, known: false },
			est_minutes: { low: 3, high: 9 },
		});
		expect(render(<Step4Setup spec={spec()} onChange={() => undefined} />)).toContain("price not known");
	});

	it("shows an estimate with unknown price and time, and per-benchmark notes in plain view", () => {
		q.estimate = okQuery({
			runs: 32,
			est_cost_usd: { low: 0, high: 0, known: false },
			est_minutes: { low: 0, high: 0, known: false },
			notes: ["model gpt-5.6-terra has no official price in the price snapshot, so its cost is unknown"],
			per_benchmark: [
				{
					benchmark: "hd-harbor-tb2",
					runs: 32,
					notes: ["no history for this benchmark yet"],
					faults_skipped: [{ fault: "broken-tool", reason: "no tool layer" }],
				},
				{
					benchmark: "voicebench",
					runs: 0,
					skipped: "agent codex cannot run benchmark voicebench: needs voice",
					notes: [],
				},
			],
		});
		const html = render(<Step4Setup spec={spec()} onChange={() => undefined} />);
		expect(html).not.toContain("Could not estimate");
		expect(html).toContain("32 runs");
		expect(html).toContain("price not known");
		expect(html).toContain("time not known");
		expect(html).not.toContain("<details");
		expect(html).toContain('data-testid="step4-est-notes"');
		expect(html).toContain("no history for this benchmark yet");
		expect(html).toContain("skipped, agent codex cannot run benchmark voicebench: needs voice");
		expect(html).toContain("broken-tool is skipped: no tool layer");
		expect(html).toContain("no official price");
	});

	it("shows the error text only on a real HTTP error", () => {
		q.estimate = {
			data: undefined,
			error: { status: 500, data: {} },
			isError: true,
			isFetching: false,
			isLoading: false,
		};
		expect(render(<Step4Setup spec={spec()} onChange={() => undefined} />)).toContain("Could not estimate this evaluation right now.");
		q.estimate = {
			data: undefined,
			error: { status: "PARSING_ERROR", error: "x" },
			isError: true,
			isFetching: false,
			isLoading: false,
		};
		expect(render(<Step4Setup spec={spec()} onChange={() => undefined} />)).not.toContain("Could not estimate");
		expect(estimateFailed({ status: 403 })).toBe(true);
		expect(estimateFailed({ status: "FETCH_ERROR" })).toBe(false);
		expect(
			estimateNotes({
				per_benchmark: [{ benchmark: "a", notes: [] }],
				notes: null,
			}),
		).toEqual([]);
	});

	it("labels an agent's default model without an empty provider prefix", () => {
		const groups = groupModels(catalog, []);
		expect(providerOfModel(groups, "deepseek-chat")).toBe("deepseek");
		expect(providerOfModel(groups, "gpt-5.6-terra")).toBe("");
		expect(modelOptionLabel("", "gpt-5.6-terra")).toBe("gpt-5.6-terra");
		expect(modelOptionLabel("OpenAI", "gpt-5.6-terra")).toBe("OpenAI: gpt-5.6-terra");
		const html = render(
			<Step4Setup
				spec={spec({
					arms: [
						{
							label: "A",
							agent: "codex",
							model: "gpt-5.6-terra",
							provider: "",
						},
					],
				})}
				onChange={() => undefined}
			/>,
		);
		expect(html).not.toContain(": gpt-5.6-terra");
		expect(html).toContain(">gpt-5.6-terra</option>");
		const listed = render(
			<Step4Setup
				spec={spec({
					arms: [{ label: "A", agent: "codex", model: "gpt-4o-mini", provider: "" }],
				})}
				onChange={() => undefined}
			/>,
		);
		expect(listed).toMatch(/<option value="openai\u0000gpt-4o-mini" selected=""/);
	});

	it("groups faults from the catalog and keeps the built-in three while loading", () => {
		let html = render(<Step4Setup spec={spec()} onChange={() => undefined} />);
		expect(html).toContain('data-testid="step4-fault-model-down"');
		expect(html).toContain('data-testid="step4-fault-group-Tools"');
		q.faults = okQuery([
			{
				id: "tool-rate-limit",
				label: "Tool rate limit",
				help: "Tools answer 429.",
				layer: "tool",
				group: "Tools",
			},
			{
				id: "compound-brownout-rate-limit",
				label: "Brownout and rate limit",
				help: "Both.",
				layer: "compound",
				group: "Combined",
			},
			{
				id: "model-slow",
				label: "Slow model",
				help: "Slow.",
				layer: "model",
				group: "Model",
			},
		]);
		html = render(<Step4Setup spec={spec({ faults: ["clean", "tool-rate-limit"] })} onChange={() => undefined} />);
		expect(html).not.toContain('data-testid="step4-fault-broken-tool"');
		expect(html).toMatch(/data-testid="step4-fault-tool-rate-limit"><input type="checkbox" class="mt-1" checked=""/);
		expect(html.indexOf("step4-fault-group-Model")).toBeLessThan(html.indexOf("step4-fault-group-Tools"));
		expect(html.indexOf("step4-fault-group-Tools")).toBeLessThan(html.indexOf("step4-fault-group-Combined"));
		expect(groupFaults([{ id: "clean", label: "", help: "", layer: "", group: "Model" }])).toEqual([]);
	});

	it("shows the power line and advice from the estimate", () => {
		const power = {
			per_benchmark: [{ benchmark: "hd-harbor-tb2", tasks: 8, decidable: true, mde_pp: 34.6 }],
			overall: { tasks: 8, mde_pp: 34.6 },
			sd_source: "assumed",
			advice: "Add 70 tasks to detect a 10 point change overall.",
		};
		q.estimate = okQuery({
			runs: 20,
			est_cost_usd: { low: 1, high: 2, known: true },
			est_minutes: { low: 1, high: 2 },
			power,
		});
		const html = render(<Step4Setup spec={spec()} onChange={() => undefined} />);
		expect(html).toContain("These settings can detect a change of about 35 points overall (35 per benchmark).");
		expect(html).toContain("Add 70 tasks");
		expect(
			powerText({
				power: {
					...power,
					per_benchmark: [{ benchmark: "x", tasks: 3, decidable: false, mde_pp: null }],
				},
			})?.line,
		).toContain("too few tasks to decide per benchmark");
		expect(powerText({ power: { ...power, overall: { tasks: 1, mde_pp: null } } })?.line).toBe(
			"These settings have too few tasks to detect a change.",
		);
		expect(powerText({})).toBeUndefined();
	});
});
describe("Step4Setup presets and slots", () => {
	const preset = (id: "quick" | "standard" | "thorough", tasks: number, trials: number, faults: string[], mde: number | null) => ({
		id,
		label: { quick: "Quick look", standard: "Standard", thorough: "Thorough" }[id],
		summary: "",
		tasks_per_benchmark: tasks,
		trials,
		faults,
		runs: tasks * trials * faults.length,
		est_cost_usd: { low: 1, high: 3, known: true },
		est_minutes: { low: 10, high: 25, known: true },
		mde_pp: mde,
		mde_per_benchmark_pp: mde,
	});

	it("shows the three sizes with runs, time, cost and power, the slots line and the library note", () => {
		q.estimate = okQuery({
			runs: 10,
			est_cost_usd: { low: 1, high: 3, known: true },
			est_minutes: { low: 10, high: 25, known: true },
			per_benchmark: [],
			from_library: true,
			slots: { concurrency: 4, org_slots: 4, parallel: 4 },
			presets: [
				preset("quick", 10, 1, ["clean"], 30.2),
				preset("standard", 30, 1, ["clean"], 16),
				preset("thorough", 50, 2, ["clean", "model-down", "broken-tool"], null),
			],
		});
		const html = render(
			<Step4Setup
				spec={{ ...defaultEvaluationSpec(), arms: [{ label: "A", agent: "codex", model: "gpt-4o-mini" }], benchmarks: ["x"] }}
				onChange={() => undefined}
			/>,
		);
		expect(tagOf(html, "step4-preset-quick")).toContain('aria-checked="true"');
		expect(tagOf(html, "step4-preset-standard")).toContain('aria-checked="false"');
		expect(html).toContain("10 runs, about 10 min to 25 min, $1.00 to $3.00");
		expect(html).toContain("sees a change of about 30 points");
		expect(html).toContain("too few tasks to see a change");
		expect(html).toContain("2 faults");
		expect(html).toContain("Up to 4 runs at once (4 per benchmark; your organization has 4 worker slots).");
		expect(html).toContain('data-testid="step4-basis"');
		expect(html).toContain("Estimated from other agents&#x27; runs of these benchmarks.");
	});
});
describe("MCP route", () => {
	it("leaves mcp_route out for direct and sets it for the Bifrost routes", () => {
		const base = defaultEvaluationSpec();
		expect("mcp_route" in withMCPRoute(base, "direct")).toBe(false);
		expect(withMCPRoute(base, "bifrost").mcp_route).toBe("bifrost");
		expect("mcp_route" in withMCPRoute(withMCPRoute(base, "bifrost_code_mode"), "direct")).toBe(false);
	});
	it("round-trips through the wizard draft", () => {
		const spec = withMCPRoute(defaultEvaluationSpec(), "bifrost_code_mode");
		const d = { ...EMPTY_DRAFT, agent: "codex", ...specToPatch(spec) };
		expect(draftToSpec(d).mcp_route).toBe("bifrost_code_mode");
		expect("mcp_route" in draftToSpec({ ...d, mcpRoute: "direct" })).toBe(false);
	});
});
describe("Step4Setup provider failover", () => {
	it("shows the toggle for one agent and blocks a bad fallback", async () => {
		const { Step4Setup: S, specReady: ready } = await import("./step4Setup");
		const one: EvaluationSpec = {
			...defaultEvaluationSpec(),
			benchmarks: ["b"],
			arms: [{ label: "A", agent: "codex", model: "openai/gpt-4o" }],
		};
		expect(render(<S spec={one} onChange={() => undefined} />)).toContain('data-testid="step4-failover-toggle"');
		const on = { ...one, failover: { enabled: true, fallbacks: [{ provider: "openai", model: "gpt-4o-mini" }] } };
		expect(render(<S spec={on} onChange={() => undefined} />)).toContain('data-testid="step4-failover-model"');
		expect(ready(on)).toBe(true);
		expect(ready({ ...on, failover: { enabled: true, fallbacks: [{ provider: "anthropic", model: "c" }] } })).toBe(false);
		const two = { ...one, arms: [...one.arms, { label: "B", agent: "other", model: "openai/gpt-4o" }] };
		expect(render(<S spec={two} onChange={() => undefined} />)).not.toContain("step4-failover");
	});
});

describe("Step4Setup fix wave F26", () => {
	afterEach(() => vi.useRealTimers());

	it("keeps the Release gate ticked through the wizard draft", () => {
		const draft = applyPatch(EMPTY_DRAFT, {
			agent: "codex",
			arms: [{ label: "A", agent: "codex", model: "gpt-4o-mini", provider: "openai" }],
			benchmarks: ["x"],
		});
		let next = draftToSpec(draft);
		render(<Step4Setup spec={next} onChange={(s) => (next = s)} />);
		// The checkbox handler sets the default gate; the wizard stores the spec in the draft.
		const ticked = draftToSpec(applyPatch(draft, specToPatch({ ...next, gate: { max_regression: 0.05 } })));
		const html = render(<Step4Setup spec={ticked} onChange={() => undefined} />);
		expect(tagOf(html, "gate-enable")).toContain('checked=""');
	});

	it("says next to a fault and above the list when it applies to no chosen benchmark", () => {
		q.estimate = okQuery({
			runs: 10,
			est_cost_usd: { low: 1, high: 2, known: true },
			est_minutes: { low: 1, high: 2 },
			per_benchmark: [
				{
					benchmark: "hd-harbor-bfcl",
					runs: 10,
					notes: ["MCP route: benchmark hd-harbor-bfcl has no MCP server"],
					faults_skipped: [{ fault: "broken-tool", reason: "benchmark hd-harbor-bfcl has no tools" }],
				},
			],
		});
		const html = render(<Step4Setup spec={spec({ faults: ["clean", "broken-tool", "model-down"] })} onChange={() => undefined} />);
		expect(html).toContain('data-testid="step4-fault-na-broken-tool"');
		expect(html).toContain("Does not apply here: benchmark hd-harbor-bfcl has no tools");
		expect(html).not.toContain('data-testid="step4-fault-na-model-down"');
		expect(html).toContain("These faults do not apply to the chosen benchmarks and add no runs: Broken tool.");
		expect(html.indexOf("step4-faults-na")).toBeLessThan(html.indexOf('data-testid="step4-fault-group-'));
		expect(html).toContain("MCP route: benchmark hd-harbor-bfcl has no MCP server");
		expect(html).not.toContain("<details");
	});

	it("only flags a fault skipped on every chosen benchmark", () => {
		const e = {
			per_benchmark: [
				{ benchmark: "a", faults_skipped: [{ fault: "broken-tool", reason: "no tools in a" }] },
				{ benchmark: "b", faults_skipped: [] },
				{ benchmark: "c", skipped: "cannot run" },
			],
		};
		expect(faultsNotApplying(e).size).toBe(0);
		expect(faultsNotApplying({ per_benchmark: [e.per_benchmark[0], e.per_benchmark[2]] }).get("broken-tool")).toBe("no tools in a");
		expect(faultsNotApplying({ per_benchmark: [] }).size).toBe(0);
	});

	it("says what the estimate is based on", () => {
		q.estimate = okQuery({ runs: 5, est_cost_usd: { low: 1, high: 2, known: true }, est_minutes: { low: 1, high: 2 }, from_library: true });
		expect(render(<Step4Setup spec={spec()} onChange={() => undefined} />)).toContain(
			"Estimated from other agents&#x27; runs of these benchmarks. Your own runs will make it more exact.",
		);
		q.estimate = okQuery({ runs: 5, est_cost_usd: { low: 1, high: 2, known: true }, est_minutes: { low: 1, high: 2 } });
		expect(render(<Step4Setup spec={spec()} onChange={() => undefined} />)).toContain("Estimated from past runs of this agent and model.");
	});

	it("shows why nothing can run in plain view when the estimate has no runs", () => {
		const reason =
			"this benchmark runs inside each task's own environment; register a container agent (agent claude-code's Dockerfile does not start FROM ${BASE_IMAGE})";
		q.estimate = okQuery({
			runs: 0,
			est_cost_usd: { low: 0, high: 0, known: true },
			est_minutes: { low: 0, high: 0 },
			per_benchmark: [{ benchmark: "hd-harbor-bfcl", runs: 0, skipped: reason }],
		});
		const html = render(<Step4Setup spec={spec()} onChange={() => undefined} />);
		expect(html).toContain('data-testid="step4-nothing-runs"');
		expect(html).toContain("hd-harbor-bfcl: ");
		expect(html).toContain("register a container agent");
		expect(html).not.toContain("<details");
		expect(nothingRunsReason(q.estimate.data as never)).toBe(`hd-harbor-bfcl: ${reason}`);
		expect(nothingRunsReason({ runs: 3, per_benchmark: [] })).toBeNull();
		expect(nothingRunsReason(undefined)).toBeNull();
	});

	it("does not refetch the presets when only Trials or Tasks change", () => {
		const a = spec();
		expect(presetsRequest({ ...a, trials: 3, tasks_per_benchmark: 50, preset: undefined })).toEqual(presetsRequest(a));
		expect(presetsRequest(a).presets).toBe(true);
		expect(presetsRequest({ ...a, faults: ["clean", "model-down"] })).not.toEqual(presetsRequest(a));
		expect(presetsRequest({ ...a, benchmarks: ["other"] })).not.toEqual(presetsRequest(a));
	});

	it("sends one estimate request for two quick Trials changes", () => {
		vi.useFakeTimers();
		const sent: unknown[] = [];
		const d = createDebouncer<ReturnType<typeof spec>>(400, (_k, v) => sent.push(v));
		const two = spec({ trials: 2 });
		const three = spec({ trials: 3 });
		d.push(JSON.stringify(two), two);
		vi.advanceTimersByTime(150);
		d.push(JSON.stringify(three), three);
		vi.advanceTimersByTime(399);
		expect(sent).toEqual([]);
		vi.advanceTimersByTime(1);
		expect(sent).toEqual([three]);
		vi.advanceTimersByTime(2000);
		expect(sent).toHaveLength(1);
	});
});
describe("Step4Setup fix wave 2 (G10)", () => {
	const estimateSection = (html: string) => html.slice(html.indexOf('data-testid="step4-estimate"'));

	it("names the real reason the spec is not ready", () => {
		expect(specProblem(spec({ benchmarks: [] }))).toBe("Pick at least one benchmark.");
		expect(specProblem(spec({ pack: "memory", benchmarks: [] }))).toBe("");
		expect(specProblem(spec({ arms: [{ label: "A", agent: "codex", model: "" }] }))).toBe("Pick a model for setup A.");
		expect(specProblem(spec({ arms: [{ label: "A", agent: "bot", model: "" }] }), new Set(["bot"]))).toBe("");
		const noFallback = spec({ failover: { enabled: true, fallbacks: [] } });
		expect(specProblem(noFallback)).toBe("Pick a fallback model.");
		expect(specReady(noFallback)).toBe(false);
		expect(specReady(spec())).toBe(true);
	});

	it("failover on without fallback shows Pick a fallback model. in the estimate box", () => {
		const html = render(<Step4Setup spec={spec({ failover: { enabled: true, fallbacks: [] } })} onChange={() => undefined} />);
		const box = estimateSection(html);
		expect(box).toContain("Pick a fallback model.");
		expect(box).not.toContain("Pick benchmarks and a model to see an estimate.");
	});

	it("worker_slots 2 and 4 picked shows the note", () => {
		q.estimate = okQuery({
			runs: 20,
			est_cost_usd: { low: 1, high: 2, known: true },
			est_minutes: { low: 10, high: 25 },
			slots: { concurrency: 4, org_slots: 0, parallel: 4, worker_slots: 2 },
		});
		const html = render(<Step4Setup spec={spec({ concurrency: 4 })} onChange={() => undefined} />);
		expect(html).toContain('data-testid="step4-concurrency-note"');
		expect(html).toContain("Your worker runs 2 runs at a time, so 4 runs at once is no faster than 2.");
		expect(render(<Step4Setup spec={spec({ concurrency: 2 })} onChange={() => undefined} />)).not.toContain("step4-concurrency-note");
		q.estimate = okQuery({ runs: 20, est_cost_usd: { low: 1, high: 2, known: true }, est_minutes: { low: 10, high: 25 } });
		expect(render(<Step4Setup spec={spec({ concurrency: 8 })} onChange={() => undefined} />)).not.toContain("step4-concurrency-note");
	});
});
describe("Step4Setup over-capacity note reads the estimate's worker_slots", () => {
	it("shows the note for the response the server sends when the worker caps the runs", () => {
		// handlers.evalSlots: a worker of 2 slots caps concurrency and parallel at 2 and reports worker_slots.
		const estimate: Partial<Estimate> = {
			runs: 20,
			est_cost_usd: { low: 1, high: 2, known: true },
			est_minutes: { low: 10, high: 25 },
			slots: { concurrency: 2, org_slots: 8, parallel: 2, worker_slots: 2 },
		};
		q.estimate = okQuery(estimate);
		const html = render(<Step4Setup spec={spec({ concurrency: 6 })} onChange={() => undefined} />);
		expect(html).toContain("Your worker runs 2 runs at a time, so 6 runs at once is no faster than 2.");
		expect(concurrencyNote(estimate.slots, 2)).toBe("");
	});
});