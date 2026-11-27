import type { Catalog, SuiteSummary } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
	agentFromLinkMissing,
	builderAgentGroups,
	builderPlanIsStale,
	defaultBuilderValues,
	defaultRunnableSuite,
	guessProvider,
	keyedProviders,
	matrixPreview,
	readyTaskMap,
	suiteMenuGroups,
	toMatrixExperiment,
	validateBuilder,
} from "./matrixBuilderSchema";

const fixture = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../../public/fixtures/catalog.json"), "utf8")) as Catalog;
const catalog: Catalog = {
	...fixture,
	agents: [...fixture.agents, { id: "codex", path: "agents/codex.yaml", adapter: "command", model: "gpt-5.4-mini" }],
};
const suite = catalog.suites[0];

describe("defaultBuilderValues", () => {
	it("starts with no benchmark (M4-91), the first agent, its own model and one empty row", () => {
		const v = defaultBuilderValues(catalog, 20261121);
		expect(v.suite).toBe("");
		expect(v.agents).toEqual(["agents/claude-code.yaml"]);
		expect(v.models).toEqual([
			{ provider: "anthropic", model: "claude-haiku-4-5" },
			{ provider: "anthropic", model: "" },
		]);
		expect(v.trials).toBe(1);
		expect(v.taskCount).toBe(5);
		// One filled model, one agent: one arm, so the form asks for a second model.
		expect(validateBuilder(v, suite).models).toBe("tooFewArms");
	});
});

describe("matrixPreview and toMatrixExperiment", () => {
	it("crosses agents with models, agents first, labelled like the planner", () => {
		const v = {
			...defaultBuilderValues(catalog, 1),
			suite: `coding-core@${suite.version}`,
			taskCount: Math.min(5, suite.tasks.length),
			agents: ["agents/claude-code.yaml", "agents/codex.yaml"],
			models: [
				{ provider: "openai" as const, model: "gpt-5.4" },
				{ provider: "anthropic" as const, model: "claude-sonnet-5" },
			],
			trials: 2,
		};
		const p = matrixPreview(v, suite);
		expect(p.arms.map((a) => [a.id, a.label])).toEqual([
			["A", "claude-code / gpt-5.4 via openai"],
			["B", "claude-code / claude-sonnet-5 via anthropic"],
			["C", "codex / gpt-5.4 via openai"],
			["D", "codex / claude-sonnet-5 via anthropic"],
		]);
		expect(p.maxRuns).toBe(4 * 1 * Math.min(5, suite.tasks.length) * 2);
		expect(validateBuilder(v, suite)).toEqual({});
		const exp = toMatrixExperiment(v, catalog);
		expect(exp.base).toBe("agents/claude-code.yaml");
		expect(exp.arms).toEqual({});
		expect(exp.matrix).toEqual({
			agents: ["agents/claude-code.yaml", "agents/codex.yaml"],
			models: [
				{ model: "gpt-5.4", provider: "openai" },
				{ model: "claude-sonnet-5", provider: "anthropic" },
			],
		});
		expect(exp.primary).toEqual({ condition: "clean", metric: "verified_success" });
		expect(exp.trials).toBeUndefined();
	});

	it("compares agents on their own models when no model is filled", () => {
		const v = { ...defaultBuilderValues(catalog, 1), agents: ["agents/claude-code.yaml", "agents/codex.yaml"], models: [] };
		expect(matrixPreview(v, suite).arms.map((a) => a.label)).toEqual(["claude-code", "codex"]);
		expect(toMatrixExperiment(v, catalog).matrix).toEqual({ agents: ["agents/claude-code.yaml", "agents/codex.yaml"] });
	});

	it("writes adaptive trials, task ids, probes and the runner pool", () => {
		const v = {
			...defaultBuilderValues(catalog, 1),
			suite: `coding-core@${suite.version}`,
			models: [
				{ provider: "openai" as const, model: "a" },
				{ provider: "openai" as const, model: "b" },
			],
			taskCount: 2,
			trials: 4,
			adaptive: true,
			adaptiveMin: 2,
			probes: [suite.probes[0]],
			runnerPool: " gpu ",
		};
		expect(validateBuilder(v, suite)).toEqual({});
		const exp = toMatrixExperiment(v, catalog);
		expect(exp.trials).toEqual({ mode: "adaptive", rule: "task", min: 2, max: 4 });
		expect(exp.conditions).toEqual([
			{ id: "clean", tasks: { ids: suite.tasks.slice(0, 2) }, trials: 4 },
			{ id: suite.probes[0], tasks: { ids: suite.tasks.slice(0, 2) }, trials: 4 },
		]);
		expect(exp.runner).toEqual({ pool: "gpu" });
		expect(builderPlanIsStale(exp, v, catalog)).toBe(false);
		expect(builderPlanIsStale(exp, { ...v, trials: 5 }, catalog)).toBe(true);
	});
});

describe("validateBuilder", () => {
	const base = {
		...defaultBuilderValues(catalog, 1),
			suite: `coding-core@${suite.version}`,
			taskCount: Math.min(5, suite.tasks.length),
		models: [
			{ provider: "openai" as const, model: "a" },
			{ provider: "openai" as const, model: "b" },
		],
	};
	it("refuses adaptive with one trial, min at or over the trials, and duplicate models", () => {
		expect(validateBuilder({ ...base, adaptive: true, trials: 1 }, suite).trials).toBe("adaptiveTrials");
		expect(validateBuilder({ ...base, adaptive: true, trials: 3, adaptiveMin: 3 }, suite).adaptiveMin).toBe("adaptiveMin");
		const dup = { ...base, models: [...base.models, { provider: "openai" as const, model: "a" }] };
		expect(validateBuilder(dup, suite)["models.2"]).toBe("modelDuplicate");
	});
	it("refuses no agent, too many arms, a zero budget and an out-of-range task count", () => {
		expect(validateBuilder({ ...base, agents: [] }, suite).agents).toBe("agentRequired");
		const many = { ...base, models: Array.from({ length: 27 }, (_, i) => ({ provider: "openai" as const, model: `m${i}` })) };
		expect(validateBuilder(many, suite).models).toBe("tooManyArms");
		expect(validateBuilder({ ...base, totalUsd: 0 }, suite).totalUsd).toBe("budgetPositive");
		expect(validateBuilder({ ...base, taskCount: suite.tasks.length + 1 }, suite).taskCount).toBe("taskCountRange");
	});
	it("guesses the provider from the model name", () => {
		expect(guessProvider("gpt-5.4")).toBe("openai");
		expect(guessProvider("claude-haiku-4-5")).toBe("anthropic");
		expect(guessProvider("gemini-3-pro")).toBe("gemini");
		expect(guessProvider("mystery")).toBeUndefined();
	});
});

describe("defaultBuilderValues with ?agent= and the org's keys (J7.new-default)", () => {
	const withDemo: Catalog = {
		...catalog,
		agents: [
			{ id: "acme-agent", path: "agents/acme-agent.yaml", adapter: "command", model: "claude-sonnet-4-5" },
			...catalog.agents,
			{ id: "noop", path: "agents/noop.yaml", adapter: "scripted", model: "" },
		],
	};
	it("starts on the agent ?agent= names, with a provider the org has a key for", () => {
		const v = defaultBuilderValues(withDemo, 1, { agent: "codex", providersWithKey: ["openai"] });
		expect(v.agents).toEqual(["agents/codex.yaml"]);
		expect(v.models[0]).toEqual({ provider: "openai", model: "gpt-5.4-mini" });
		expect(v.models[1]).toEqual({ provider: "openai", model: "" });
	});
	it("takes a keyed provider over the guessed one when the guess has no key", () => {
		const v = defaultBuilderValues(withDemo, 1, { agent: "claude-code", providersWithKey: ["openrouter"] });
		expect(v.models[0]).toEqual({ provider: "openrouter", model: "claude-haiku-4-5" });
	});
	it("keeps the guessed provider when it has a key, and guesses when no key is known", () => {
		expect(defaultBuilderValues(withDemo, 1, { agent: "claude-code", providersWithKey: ["openai", "anthropic"] }).models[0].provider).toBe(
			"anthropic",
		);
		expect(defaultBuilderValues(withDemo, 1, { agent: "codex", providersWithKey: [] }).models[0].provider).toBe("openai");
	});
	it("starts on the first shown agent, never a demo agent, when ?agent= is missing or unknown", () => {
		const shown = ["codex", "claude-code"];
		expect(defaultBuilderValues(withDemo, 1, { shownAgents: shown }).agents).toEqual(["agents/codex.yaml"]);
		expect(defaultBuilderValues(withDemo, 1, { agent: "nope", shownAgents: shown }).agents).toEqual(["agents/codex.yaml"]);
		expect(defaultBuilderValues(withDemo, 1, { agent: "noop", shownAgents: shown }).agents).toEqual(["agents/codex.yaml"]);
	});
	it("groups the catalog agents as My agents does and leaves the rest out", () => {
		const g = builderAgentGroups(withDemo, { yours: ["codex"], baselines: ["claude-code"] });
		expect(g.yours.map((a) => a.id)).toEqual(["codex"]);
		expect(g.builtIn.map((a) => a.id)).toEqual(["claude-code"]);
		expect(builderAgentGroups(withDemo, undefined).yours.map((a) => a.id)).toEqual(["acme-agent", "claude-code", "codex", "noop"]);
	});
});

describe("keyedProviders", () => {
	it("lists providers with a key that is not invalid, once each", () => {
		expect(
			keyedProviders([
				{ provider: "anthropic", status: "invalid" },
				{ provider: "openai", status: "ok" },
				{ provider: "openai", status: "untested" },
				{ provider: "gemini", status: "untested" },
			] as never),
		).toEqual(["openai", "gemini"]);
	});
});
// Fix wave 2 (N39, N42, N155): defaults follow readiness and the org's agents; forms send only ready tasks.
describe("ready tasks (N39, N42)", () => {
	const ids = (n: number, prefix: string) => Array.from({ length: n }, (_, i) => `${prefix}${i + 1}`);
	const many: Catalog = {
		...catalog,
		suites: [
			{ id: "acme-desktop", version: "1.0.0", tasks: ["file-inbox", "calendar"], probes: ["model-outage"] },
			{ id: "coding-core", version: "1.0.0", tasks: ids(4, "t"), probes: ["stale-notes"] },
			{ id: "hd-harbor-bfcl", version: "2.0.0", tasks: ids(20, "b"), probes: [] },
			{ id: "hd-drafty", version: "1.0.0", tasks: ids(30, "d"), probes: [] },
		],
	};
	const summary = (id: string, version: string, tasks: { id: string; status: string; stale?: boolean }[]) =>
		({ id, version, tasks: tasks.map((t) => ({ stale: false, ...t })) }) as unknown as SuiteSummary;
	const suites: SuiteSummary[] = [
		summary("acme-desktop", "1.0.0", [
			{ id: "file-inbox", status: "draft" },
			{ id: "calendar", status: "admitted" },
		]),
		summary(
			"coding-core",
			"1.0.0",
			ids(4, "t").map((id) => ({ id, status: "admitted" })),
		),
		summary("hd-harbor-bfcl", "2.0.0", [...ids(19, "b").map((id) => ({ id, status: "admitted" })), { id: "b20", status: "draft" }]),
		summary(
			"hd-drafty",
			"1.0.0",
			ids(30, "d").map((id, i) => ({ id, status: i === 0 ? "admitted" : "draft" })),
		),
	];
	const ready = readyTaskMap(many, suites);

	it("counts only admitted tasks whose files still match", () => {
		const stale = readyTaskMap(many, [summary("acme-desktop", "1.0.0", [{ id: "calendar", status: "admitted", stale: true }])]);
		expect(stale?.get("acme-desktop@1.0.0")).toEqual({ ids: [], total: 2 });
		expect(ready?.get("hd-harbor-bfcl@2.0.0")?.ids).toHaveLength(19);
		expect(ready?.get("acme-desktop@1.0.0")).toEqual({ ids: ["calendar"], total: 2 });
		expect(readyTaskMap(many, undefined)).toBeUndefined();
	});

	it("defaults to the non-practice benchmark with the most ready tasks, skipping coding-core and acme-*", () => {
		expect(defaultRunnableSuite(many, ready)?.id).toBe("hd-harbor-bfcl");
		// M4-91: the builder itself starts empty; the A/B form still starts on this benchmark.
		expect(defaultBuilderValues(many, 1, { ready }).suite).toBe("");
	});

	it("falls back to the practice benchmark with the most ready tasks, then the first", () => {
		// No benchmark that is not practice has 2 ready tasks: hd-harbor-bfcl has none, hd-drafty one.
		const practiceOnly = readyTaskMap(many, [
			...suites.slice(0, 2),
			summary("hd-harbor-bfcl", "2.0.0", [{ id: "b1", status: "draft" }]),
			suites[3],
		]);
		expect(defaultRunnableSuite(many, practiceOnly)?.id).toBe("coding-core");
		const noneReady = readyTaskMap(
			many,
			suites.map((x) => ({ ...x, tasks: x.tasks.map((t) => ({ ...t, status: "draft" as const })) })),
		);
		expect(defaultRunnableSuite(many, noneReady)?.id).toBe("acme-desktop");
	});

	it("sends only ready ids for all tasks and takes the first N ready ids", () => {
		const v = {
			...defaultBuilderValues(many, 1, { ready }),
			suite: "acme-desktop@1.0.0",
			agents: ["agents/claude-code.yaml", "agents/codex.yaml"],
			taskScope: "all" as const,
		};
		const exp = toMatrixExperiment(v, many, ready);
		expect(exp.conditions[0].tasks).toEqual({ ids: ["calendar"] });
		const first = toMatrixExperiment({ ...v, suite: "hd-harbor-bfcl@2.0.0", taskScope: "first", taskCount: 19 }, many, ready);
		expect(first.conditions[0].tasks).toEqual({ ids: ids(19, "b") });
		// Every task ready: "all" stays all.
		expect(toMatrixExperiment({ ...v, suite: "coding-core@1.0.0" }, many, ready).conditions[0].tasks).toEqual({ all: true });
		// The task count range uses the ready count.
		const s = many.suites[2];
		expect(validateBuilder({ ...v, suite: "hd-harbor-bfcl@2.0.0", taskScope: "first", taskCount: 20 }, s, ready).taskCount).toBe(
			"taskCountRange",
		);
		expect(validateBuilder({ ...v, suite: "hd-harbor-bfcl@2.0.0", taskScope: "first", taskCount: 19 }, s, ready).taskCount).toBeUndefined();
	});

	it("lists ready benchmarks first and benchmarks with no ready task apart", () => {
		const none = readyTaskMap(many, [summary("acme-desktop", "1.0.0", [{ id: "file-inbox", status: "draft" }]), ...suites.slice(1)]);
		const groups = suiteMenuGroups(many, none);
		expect(groups.ready.map((s) => s.id)).toEqual(["coding-core", "hd-harbor-bfcl", "hd-drafty"]);
		expect(groups.notReady.map((s) => s.id)).toEqual(["acme-desktop"]);
		expect(suiteMenuGroups(many, undefined).notReady).toEqual([]);
	});
});

describe("agentFromLinkMissing (N155)", () => {
	it("is true only for a linked agent the builder does not list", () => {
		expect(agentFromLinkMissing("zz-nope", ["codex"])).toBe(true);
		expect(agentFromLinkMissing("codex", ["codex"])).toBe(false);
		expect(agentFromLinkMissing("agents/codex.yaml", ["codex"])).toBe(false);
		expect(agentFromLinkMissing(null, ["codex"])).toBe(false);
		expect(agentFromLinkMissing("  ", ["codex"])).toBe(false);
	});
});