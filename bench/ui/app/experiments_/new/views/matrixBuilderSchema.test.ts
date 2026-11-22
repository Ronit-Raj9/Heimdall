import type { Catalog } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
	builderAgentGroups,
	builderPlanIsStale,
	defaultBuilderValues,
	guessProvider,
	keyedProviders,
	matrixPreview,
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
	it("starts on coding-core with the first agent, its own model and one empty row", () => {
		const v = defaultBuilderValues(catalog, 20261121);
		expect(v.suite).toBe(`coding-core@${suite.version}`);
		expect(v.agents).toEqual(["agents/claude-code.yaml"]);
		expect(v.models).toEqual([
			{ provider: "anthropic", model: "claude-haiku-4-5" },
			{ provider: "anthropic", model: "" },
		]);
		expect(v.trials).toBe(1);
		expect(v.taskCount).toBe(Math.min(5, suite.tasks.length));
		// One filled model, one agent: one arm, so the form asks for a second model.
		expect(validateBuilder(v, suite).models).toBe("tooFewArms");
	});
});

describe("matrixPreview and toMatrixExperiment", () => {
	it("crosses agents with models, agents first, labelled like the planner", () => {
		const v = {
			...defaultBuilderValues(catalog, 1),
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