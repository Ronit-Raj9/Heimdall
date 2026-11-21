import type { ModelRef, ProviderId } from "@/components/models/modelPicker";
import type { Catalog, CatalogAgent, CatalogSuite, Experiment } from "@/lib/types/bench";
import { suiteRef } from "./experimentFormSchema";

/**
 * The no-YAML experiment builder (web parity L13): agents x models on one suite. It compiles to
 * the experiment's `matrix:` sugar (docs/model-matrix.md), which the planner expands into arms
 * A, B, C... in the same order the preview shows.
 */
export interface BuilderValues {
	name: string;
	/** "<id>@<version>" */
	suite: string;
	/** Agent config paths, in arm order. The first is the experiment's base. */
	agents: string[];
	/** Models crossed with every agent. Empty compares the agents with their own models. */
	models: ModelRef[];
	/** "first" runs the first taskCount tasks of the suite; "all" runs every task. */
	taskScope: "first" | "all";
	taskCount: number;
	/** Trials per task. With adaptive trials this is the maximum. */
	trials: number;
	adaptive: boolean;
	/** Adaptive only: trials every task always runs (rule task). */
	adaptiveMin: number;
	/** Probe conditions run beside clean. */
	probes: string[];
	totalUsd: number;
	concurrency: number;
	seed: number;
	/** Hosted mode: runner pool; empty uses the default pool. */
	runnerPool: string;
	allowUnpriced: boolean;
}

/** The generated arms cap (engine/matrixplan.go maxMatrixArms). */
export const MAX_ARMS = 26;

export type BuilderField = keyof BuilderValues | `models.${number}`;
/** Builder error keys, under the "builder" namespace's errors.* entries. */
export type BuilderErrorKey =
	| "nameRequired"
	| "suiteRequired"
	| "agentRequired"
	| "modelEmpty"
	| "modelDuplicate"
	| "tooFewArms"
	| "tooManyArms"
	| "taskCountRange"
	| "trialsRange"
	| "adaptiveTrials"
	| "adaptiveMin"
	| "budgetPositive"
	| "concurrencyRange"
	| "seedInt";
export type BuilderErrors = Partial<Record<BuilderField, BuilderErrorKey>>;

/** The model's usual gateway provider, from its name; undefined when the name does not say. */
export function guessProvider(model: string): ProviderId | undefined {
	const m = model.toLowerCase();
	if (/^(gpt|o\d|chatgpt|codex)/.test(m)) return "openai";
	if (m.startsWith("claude")) return "anthropic";
	if (m.startsWith("gemini")) return "gemini";
	if (m.includes("/")) return "openrouter";
	return undefined;
}

/** The suite to start from: coding-core when the project has it (the starter suite), else the first. */
export function defaultSuite(catalog: Catalog): CatalogSuite | undefined {
	const suites = catalog.suites ?? [];
	return suites.find((s) => s.id === "coding-core") ?? suites[0];
}

/**
 * Builder defaults: the starter suite, the first agent, its own model plus one empty row to fill
 * in (a matrix needs two arms), 5 tasks x 1 trial, clean only.
 */
export function defaultBuilderValues(catalog: Catalog, seed: number): BuilderValues {
	const suite = defaultSuite(catalog);
	const agent = catalog.agents?.[0];
	const models: ModelRef[] = [];
	if (agent?.model) models.push({ provider: guessProvider(agent.model) ?? "openai", model: agent.model });
	models.push({ provider: models[0]?.provider ?? "openai", model: "" });
	return {
		name: `compare-${seed}`,
		suite: suite ? suiteRef(suite) : "",
		agents: agent ? [agent.path] : [],
		models,
		taskScope: "first",
		taskCount: Math.min(5, Math.max(1, suite?.tasks.length ?? 5)),
		trials: 1,
		adaptive: false,
		adaptiveMin: 1,
		probes: [],
		totalUsd: 10,
		concurrency: 4,
		seed,
		runnerPool: "",
		allowUnpriced: false,
	};
}

export interface PreviewArm {
	id: string;
	label: string;
	agent: string;
	model?: ModelRef;
}

export interface MatrixPreview {
	arms: PreviewArm[];
	/** Arms x conditions x tasks x trials: the most runs the experiment can start. */
	maxRuns: number;
	tasks: number;
	conditions: number;
}

function agentName(path: string): string {
	const base = path.slice(path.lastIndexOf("/") + 1);
	const dot = base.lastIndexOf(".");
	return dot > 0 ? base.slice(0, dot) : base;
}

/** The models that count: rows with a model name, trimmed. */
export function filledModels(v: BuilderValues): ModelRef[] {
	return v.models.map((m) => ({ ...m, model: m.model.trim() })).filter((m) => m.model !== "");
}

/** The tasks every condition runs. */
export function selectedTasks(v: BuilderValues, suite: CatalogSuite | undefined): string[] {
	const tasks = suite?.tasks ?? [];
	return v.taskScope === "all" ? [...tasks] : tasks.slice(0, Math.max(0, v.taskCount));
}

/** The arms the planner will generate, labelled as engine/matrixplan.go labels them. */
export function matrixPreview(v: BuilderValues, suite: CatalogSuite | undefined): MatrixPreview {
	const models = filledModels(v);
	const withAgent = v.agents.length > 1;
	const withModel = models.length > 0;
	const agents = withAgent ? v.agents : v.agents.slice(0, 1);
	const rows: (ModelRef | undefined)[] = withModel ? models : [undefined];
	const arms: PreviewArm[] = [];
	for (const agent of agents) {
		for (const model of rows) {
			const label: string[] = [];
			if (withAgent) label.push(agentName(agent));
			if (model) label.push(`${model.model} via ${model.provider}`);
			arms.push({ id: armId(arms.length), label: label.join(" / "), agent, model });
		}
	}
	const tasks = selectedTasks(v, suite).length;
	const conditions = 1 + v.probes.length;
	return { arms, tasks, conditions, maxRuns: arms.length * conditions * tasks * Math.max(0, v.trials) };
}

function armId(i: number): string {
	return i < MAX_ARMS ? String.fromCharCode(65 + i) : `#${i + 1}`;
}

const isInt = (n: number) => Number.isInteger(n);

/** Field errors in the values, as builder error keys. Empty means the form can be planned. */
export function validateBuilder(v: BuilderValues, suite: CatalogSuite | undefined): BuilderErrors {
	const e: BuilderErrors = {};
	if (!v.name.trim()) e.name = "nameRequired";
	if (!v.suite || !suite) e.suite = "suiteRequired";
	if (v.agents.length === 0) e.agents = "agentRequired";
	const seen = new Set<string>();
	v.models.forEach((m, i) => {
		const name = m.model.trim();
		// An empty row is fine while another row is filled; it is just ignored.
		if (!name) return;
		const key = `${name}\u0000${m.provider}`;
		if (seen.has(key)) e[`models.${i}`] = "modelDuplicate";
		seen.add(key);
	});
	const arms = matrixPreview(v, suite).arms.length;
	if (!e.agents) {
		if (arms < 2) e.models = filledModels(v).length === 0 && v.models.length > 0 ? "modelEmpty" : "tooFewArms";
		else if (arms > MAX_ARMS) e.models = "tooManyArms";
	}
	const max = suite?.tasks.length ?? 0;
	if (v.taskScope === "first" && (!isInt(v.taskCount) || v.taskCount < 1 || (max > 0 && v.taskCount > max))) e.taskCount = "taskCountRange";
	if (!isInt(v.trials) || v.trials < 1 || v.trials > 50) e.trials = "trialsRange";
	else if (v.adaptive && v.trials < 2) e.trials = "adaptiveTrials";
	if (v.adaptive && (!isInt(v.adaptiveMin) || v.adaptiveMin < 1 || v.adaptiveMin >= v.trials)) e.adaptiveMin = "adaptiveMin";
	if (!(v.totalUsd > 0)) e.totalUsd = "budgetPositive";
	if (!isInt(v.concurrency) || v.concurrency < 1 || v.concurrency > 64) e.concurrency = "concurrencyRange";
	if (!isInt(v.seed) || v.seed < 0) e.seed = "seedInt";
	return e;
}

/** The primary metric: verified_success when the catalog lists it, else its first metric. */
export function primaryMetric(catalog: Catalog): string {
	const metrics = catalog.metrics ?? [];
	return (metrics.find((m) => m.name === "verified_success") ?? metrics[0])?.name ?? "verified_success";
}

/** The experiment the builder describes: a matrix experiment the planner expands into arms. */
export function toMatrixExperiment(v: BuilderValues, catalog: Catalog): Experiment {
	const suite = catalog.suites.find((s) => suiteRef(s) === v.suite);
	const all = v.taskScope === "all" || (suite !== undefined && v.taskCount >= suite.tasks.length);
	const tasks = all ? { all: true } : { ids: selectedTasks(v, suite) };
	const models = filledModels(v).map((m) => ({ model: m.model, provider: m.provider }));
	const matrix: Record<string, unknown> = {};
	if (v.agents.length > 1) matrix.agents = [...v.agents];
	if (models.length > 0) matrix.models = models;
	if (v.allowUnpriced) matrix.allow_unpriced = true;
	return {
		id: "",
		name: v.name.trim(),
		suite: v.suite,
		base: v.agents[0] ?? "",
		arms: {},
		matrix,
		primary: { condition: "clean", metric: primaryMetric(catalog) },
		guards: [{ rule: "no_new_integrity_violation" }],
		conditions: ["clean", ...v.probes].map((id) => ({ id, tasks: { ...tasks }, trials: v.trials })),
		budget: { total_usd: v.totalUsd },
		verdict: { policy: "paired_signflip", alpha: 0.05, assumed_sd: 0.3 },
		seed: v.seed,
		concurrency: v.concurrency,
		manifest_mode: "agent_exposure",
		...(v.adaptive ? { trials: { mode: "adaptive", rule: "task", min: v.adaptiveMin, max: v.trials } } : {}),
		...(v.runnerPool.trim() ? { runner: { pool: v.runnerPool.trim() } } : {}),
	};
}

/** Whether a planned experiment no longer matches the form. */
export function builderPlanIsStale(planned: Experiment, v: BuilderValues, catalog: Catalog): boolean {
	return JSON.stringify(toMatrixExperiment(v, catalog)) !== JSON.stringify(planned);
}

/** The agent the catalog lists at path, for its label. */
export function catalogAgent(catalog: Catalog, path: string): CatalogAgent | undefined {
	return catalog.agents.find((a) => a.path === path);
}
