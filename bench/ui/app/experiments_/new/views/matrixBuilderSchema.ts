import type { ModelRef, ProviderId } from "@/components/models/modelPicker";
import { isPracticeExperiment } from "@/components/experiments/experiments.utils";
import type { Catalog, CatalogAgent, CatalogSuite, Experiment, SuiteSummary } from "@/lib/types/bench";
import type { ModelKey } from "@/lib/types/models";
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

/**
 * The tasks of each benchmark that can run, keyed by "<id>@<version>": admitted tasks whose files still
 * match the lock and that are not kept out of plans, in the catalog's task order. `total` is the
 * catalog's task count. A benchmark missing from the map is not known, and every task of it counts.
 */
export type ReadyTasks = ReadonlyMap<string, { ids: readonly string[]; total: number }>;

/**
 * Ready tasks per catalog benchmark from GET /suites; undefined while the list is not known. The light list
 * (fields=light) names them in ready_task_ids; ready_known false means the server cannot tell, so the benchmark
 * gets no entry (unknown, never zero). An entry without ready_known (the full list) is read from its tasks.
 */
export function readyTaskMap(catalog: Catalog, suites: readonly SuiteSummary[] | undefined): ReadyTasks | undefined {
	if (!suites) return undefined;
	const out = new Map<string, { ids: readonly string[]; total: number }>();
	for (const s of catalog.suites ?? []) {
		const summary = suites.find((x) => x.id === s.id && x.version === s.version);
		if (!summary || summary.ready_known === false) continue;
		const ok = new Set(
			summary.ready_known
				? (summary.ready_task_ids ?? [])
				: (summary.tasks ?? []).filter((t) => t.status === "admitted" && !t.stale && !t.quarantine).map((t) => t.id),
		);
		const tasks = s.tasks ?? [];
		out.set(suiteRef(s), { ids: tasks.filter((id) => ok.has(id)), total: tasks.length });
	}
	return out;
}

/**
 * True when the ready tasks of suite cannot be known: the list arrived (ready is set) but has no entry for it.
 * The forms then block Plan instead of sending tasks that may be drafts (N195).
 */
export function readyUnknown(suite: CatalogSuite | undefined, ready: ReadyTasks | undefined): boolean {
	return suite !== undefined && ready !== undefined && !ready.has(suiteRef(suite));
}

/**
 * The values with every field the person has not touched taken from next (L2): when the defaults change (the
 * ready list arrived) the form keeps what was typed and fills the rest, without starting again.
 */
export function fillUntouched<T extends object>(current: T, next: T, touched: (key: keyof T) => boolean): T {
	const out = { ...current };
	for (const key of Object.keys(next) as (keyof T)[]) {
		if (!touched(key)) out[key] = next[key];
	}
	return out;
}

/** The tasks of suite that can run: its ready ids when known, else every task. */
export function readyIds(suite: CatalogSuite | undefined, ready: ReadyTasks | undefined): readonly string[] {
	if (!suite) return [];
	return ready?.get(suiteRef(suite))?.ids ?? suite.tasks ?? [];
}

/** True when ready is known for suite and some of its tasks cannot run. */
export function someNotReady(suite: CatalogSuite | undefined, ready: ReadyTasks | undefined): boolean {
	const entry = suite ? ready?.get(suiteRef(suite)) : undefined;
	return entry !== undefined && entry.ids.length < entry.total;
}

/**
 * The suite to start from: the benchmark that is not practice with the most ready tasks (at least 2),
 * ties by id; else the practice benchmark with the most ready tasks; else the first.
 */
export function defaultRunnableSuite(catalog: Catalog, ready?: ReadyTasks): CatalogSuite | undefined {
	const suites = catalog.suites ?? [];
	const count = (s: CatalogSuite) => readyIds(s, ready).length;
	const best = (list: CatalogSuite[]) =>
		[...list].sort((a, b) => count(b) - count(a) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))[0] as CatalogSuite | undefined;
	const real = suites.filter((s) => !isPracticeExperiment({ suite: s.id }) && count(s) >= 2);
	if (real.length > 0) return best(real);
	const practice = suites.filter((s) => isPracticeExperiment({ suite: s.id }) && count(s) > 0);
	return best(practice) ?? suites[0];
}

/** The benchmark menu: benchmarks with a ready task first, in catalog order, then those with none. */
export function suiteMenuGroups(catalog: Catalog, ready: ReadyTasks | undefined): { ready: CatalogSuite[]; notReady: CatalogSuite[] } {
	const suites = catalog.suites ?? [];
	const none = (s: CatalogSuite) => readyIds(s, ready).length === 0 && ready?.has(suiteRef(s)) === true;
	return { ready: suites.filter((s) => !none(s)), notReady: suites.filter(none) };
}

/** What the page knows beside the catalog when it opens the builder. */
export interface BuilderDefaultsOptions {
	/** ?agent=: the agent id (or config path) to start on. */
	agent?: string | null;
	/** Agent ids the builder lists (My agents' own and built-in agents); absent lists every catalog agent. */
	shownAgents?: readonly string[];
	/** Providers the org has a working key for, in the order to prefer them. */
	providersWithKey?: readonly ProviderId[];
	/** Ready tasks per benchmark (GET /suites); absent counts every task. */
	ready?: ReadyTasks;
}

/**
 * The provider to run model with: the one its name suggests when the org has a key for it, else the
 * first provider with a working key; with no key known, the guess from the name.
 */
export function defaultProvider(model: string, providersWithKey: readonly ProviderId[] = []): ProviderId {
	const guess = guessProvider(model);
	if (providersWithKey.length === 0) return guess ?? "openai";
	return guess && providersWithKey.includes(guess) ? guess : providersWithKey[0];
}

/** Providers the org has a working key for (any key not marked invalid), in key order. */
export function keyedProviders(keys: readonly Pick<ModelKey, "provider" | "status">[]): ProviderId[] {
	const out: ProviderId[] = [];
	for (const k of keys) if (k.status !== "invalid" && !out.includes(k.provider)) out.push(k.provider);
	return out;
}

/** The agent to start on: the one ?agent= names when the builder lists it, else the first listed. */
export function startAgent(catalog: Catalog, opts: BuilderDefaultsOptions): CatalogAgent | undefined {
	const agents = catalog.agents ?? [];
	// In the listed order (the org's own agents before the built-in ones).
	const shown = opts.shownAgents ? opts.shownAgents.flatMap((id) => agents.filter((a) => a.id === id)) : agents;
	const wanted = opts.agent?.trim();
	return (
		(wanted ? shown.find((a) => a.id === wanted || a.path === wanted) : undefined) ?? shown[0] ?? (opts.shownAgents ? undefined : agents[0])
	);
}

/**
 * True when ?agent= names an agent the builder does not list (N155), so the page says it started on
 * another one. shown holds agent ids; a config path "agents/<id>.yaml" of a listed agent counts too.
 */
export function agentFromLinkMissing(agentParam: string | null | undefined, shown: readonly string[]): boolean {
	const wanted = agentParam?.trim();
	if (!wanted) return false;
	return !shown.some((id) => id === wanted || `agents/${id}.yaml` === wanted);
}

/**
 * Builder defaults: the benchmark with the most ready tasks (defaultRunnableSuite), the agent ?agent= names (else the first listed agent), its own
 * model on a provider the org has a key for plus one empty row to fill in (a matrix needs two arms),
 * 5 tasks x 1 trial, clean only.
 */
export function defaultBuilderValues(catalog: Catalog, seed: number, opts: BuilderDefaultsOptions = {}): BuilderValues {
	const suite = defaultRunnableSuite(catalog, opts.ready);
	const agent = startAgent(catalog, opts);
	const keyed = opts.providersWithKey ?? [];
	const models: ModelRef[] = [];
	if (agent?.model) models.push({ provider: defaultProvider(agent.model, keyed), model: agent.model });
	models.push({ provider: models[0]?.provider ?? keyed[0] ?? "openai", model: "" });
	return {
		name: `compare-${seed}`,
		suite: suite ? suiteRef(suite) : "",
		agents: agent ? [agent.path] : [],
		models,
		taskScope: "first",
		taskCount: Math.min(5, Math.max(1, suite ? readyIds(suite, opts.ready).length : 5)),
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

/** The tasks every condition runs: the ready tasks of the suite, all of them or the first taskCount. */
export function selectedTasks(v: BuilderValues, suite: CatalogSuite | undefined, ready?: ReadyTasks): string[] {
	const tasks = readyIds(suite, ready);
	return v.taskScope === "all" ? [...tasks] : tasks.slice(0, Math.max(0, v.taskCount));
}

/** The arms the planner will generate, labelled as engine/matrixplan.go labels them. */
export function matrixPreview(v: BuilderValues, suite: CatalogSuite | undefined, ready?: ReadyTasks): MatrixPreview {
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
	const tasks = selectedTasks(v, suite, ready).length;
	const conditions = 1 + v.probes.length;
	return { arms, tasks, conditions, maxRuns: arms.length * conditions * tasks * Math.max(0, v.trials) };
}

function armId(i: number): string {
	return i < MAX_ARMS ? String.fromCharCode(65 + i) : `#${i + 1}`;
}

const isInt = (n: number) => Number.isInteger(n);

/** Field errors in the values, as builder error keys. Empty means the form can be planned. */
export function validateBuilder(v: BuilderValues, suite: CatalogSuite | undefined, ready?: ReadyTasks): BuilderErrors {
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
	const arms = matrixPreview(v, suite, ready).arms.length;
	if (!e.agents) {
		if (arms < 2) e.models = filledModels(v).length === 0 && v.models.length > 0 ? "modelEmpty" : "tooFewArms";
		else if (arms > MAX_ARMS) e.models = "tooManyArms";
	}
	const max = readyIds(suite, ready).length;
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
export function toMatrixExperiment(v: BuilderValues, catalog: Catalog, ready?: ReadyTasks): Experiment {
	const suite = catalog.suites.find((s) => suiteRef(s) === v.suite);
	const runnable = readyIds(suite, ready);
	const all = v.taskScope === "all" || (suite !== undefined && v.taskCount >= runnable.length);
	// N42: "all" on a benchmark with tasks that are not ready names the ready ones, so the plan is not refused.
	const tasks = all && !someNotReady(suite, ready) ? { all: true } : { ids: all ? [...runnable] : selectedTasks(v, suite, ready) };
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
export function builderPlanIsStale(planned: Experiment, v: BuilderValues, catalog: Catalog, ready?: ReadyTasks): boolean {
	return JSON.stringify(toMatrixExperiment(v, catalog, ready)) !== JSON.stringify(planned);
}

/** The agent the catalog lists at path, for its label. */
export function catalogAgent(catalog: Catalog, path: string): CatalogAgent | undefined {
	return catalog.agents.find((a) => a.path === path);
}

/** Agent ids of GET /agents as My agents splits them (the org's own, then the built-in ones). */
export interface BuilderAgentIds {
	yours: readonly string[];
	baselines: readonly string[];
}

/**
 * The catalog agents the builder lists, grouped as My agents shows them: the org's own, then the
 * built-in ones. Any other catalog agent (example and test agents) is left out. Without the agents
 * list every catalog agent is listed as the org's own.
 */
export function builderAgentGroups(catalog: Catalog, ids: BuilderAgentIds | undefined): { yours: CatalogAgent[]; builtIn: CatalogAgent[] } {
	const agents = catalog.agents ?? [];
	if (!ids) return { yours: [...agents], builtIn: [] };
	return {
		yours: agents.filter((a) => ids.yours.includes(a.id)),
		builtIn: agents.filter((a) => ids.baselines.includes(a.id) && !ids.yours.includes(a.id)),
	};
}
/**
 * The model rows with the prefilled model names emptied, when the rows are still exactly the
 * builder's defaults (p06-6): with two or more agents an untouched default model would override each
 * agent's own model. Providers are kept. Rows the person changed come back unchanged.
 */
export function clearDefaultModels(models: ModelRef[], defaults: readonly ModelRef[]): ModelRef[] {
	const same =
		models.length === defaults.length && models.every((m, i) => m.provider === defaults[i].provider && m.model === defaults[i].model);
	return same ? models.map((m) => ({ ...m, model: "" })) : models;
}