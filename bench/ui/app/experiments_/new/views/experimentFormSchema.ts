import type benchEn from "@/locales/en/bench.json";
import type { Catalog, CatalogSuite, Experiment, JsonValue } from "@/lib/types/bench";
import { z } from "zod";
// A cycle with matrixBuilderSchema (it imports suiteRef): both only call each other's functions at run time.
import { defaultRunnableSuite, startAgent, type BuilderDefaultsOptions, type ReadyTasks } from "./matrixBuilderSchema";

/** A t() key for a validation message. Zod messages are keys, translated where the error renders. */
export type ValidationKey = `experiments.new.validation.${keyof (typeof benchEn)["experiments"]["new"]["validation"]}`;

/** A validation message from the ufl2 catalog (round 5 lane L2); ErrorText reads the prefix. */
const msg2 = (key: "alphaRange" | "modelsRequired" | "specJson"): ValidationKey => `ufl2.validation.${key}` as unknown as ValidationKey;

const msg = (key: keyof (typeof benchEn)["experiments"]["new"]["validation"]): ValidationKey => `experiments.new.validation.${key}`;

/** Dotted agent-config path, for example notes.user_file or budgets.turns (DESIGN.md 8.6). */
const DOTTED_PATH = /^[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z0-9_-]+)*$/;

/** Arm B's change kinds. custom is the raw path and JSON value, kept under Advanced. */
export const CHANGE_KINDS = ["routing", "prompt_cache", "failover", "code_mode", "team", "custom"] as const;
export type ChangeKind = (typeof CHANGE_KINDS)[number];

export function splitModels(raw: string): string[] {
	return raw
		.split(",")
		.map((m) => m.trim())
		.filter(Boolean);
}

/** The extra spec box as an object, or null when it is not a JSON object. */
export function parseExtraSpec(raw: string): Record<string, JsonValue> | null {
	if (!raw.trim()) return {};
	try {
		const v = JSON.parse(raw) as unknown;
		return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, JsonValue>) : null;
	} catch {
		return null;
	}
}

/**
 * The agent-config overrides of arm B (engine.ResolveAgentConfig paths, spec/agent.schema.json).
 * routing: gateway.routing; failover: gateway.fallbacks; prompt caching and Code Mode: gateway.bifrost.
 */
export function armBSet(v: Pick<ExperimentFormValues, "changeKind" | "overridePath" | "overrideValue" | "changeModels" | "routingStrategy" | "cacheMode" | "codeMode" | "teamSize">): Record<string, JsonValue> {
	const models = splitModels(v.changeModels);
	switch (v.changeKind) {
		case "routing": {
			const targets: JsonValue[] = models.map((model): JsonValue => (v.routingStrategy === "weighted" ? { model, weight: 1 } : { model }));
			if (v.routingStrategy === "fallback_chain") {
				return { "gateway.routing": { strategy: "fallback_chain", targets: targets.slice(0, 1), fallbacks: targets.slice(1) } };
			}
			return { "gateway.routing": { strategy: v.routingStrategy, targets: v.routingStrategy === "fixed" ? targets.slice(0, 1) : targets } };
		}
		case "failover":
			return { "gateway.fallbacks": models };
		case "prompt_cache":
			return { "gateway.bifrost.prompt_cache": v.cacheMode };
		case "code_mode":
			return { "gateway.bifrost.mcp.code_mode": v.codeMode };
		case "team":
			return {
				topology: "team",
				team: Array.from({ length: v.teamSize }, (_, i) => (i === 0 ? { id: "lead", role: "lead" } : { id: `worker${i}`, role: "worker" })),
			};
		default:
			return { [v.overridePath.trim()]: parseOverrideValue(v.overrideValue) };
	}
}

/** The advanced spec blocks the form sets (U2 section 2), then the extra JSON box on top. */
export function advancedSpec(v: ExperimentFormValues): Partial<Experiment> {
	const out: Record<string, unknown> = {};
	if (v.aa) out.aa = true;
	if (v.fairness) out.fairness = "equal_budget";
	if (v.realism) out.realism = v.realism;
	if (v.egress) out.egress = v.egress;
	if (v.sandbagging) out.framing_pairs = [{ id: "sb", kind: "sandbagging", base: "B" }];
	if (v.attribution) out.attribution = { replay: true };
	return { ...out, ...(parseExtraSpec(v.extraSpec) ?? {}) } as Partial<Experiment>;
}

const conditionSchema = z.object({
	/** "clean" or a probe id. */
	condition: z.string(),
	enabled: z.boolean(),
	allTasks: z.boolean(),
	taskIds: z.array(z.string()),
	trials: z
		.number({ error: msg("trialsRange") })
		.int(msg("trialsRange"))
		.min(1, msg("trialsRange"))
		.max(50, msg("trialsRange")),
});

/** The new-experiment form. One arm-B override: one variable per experiment. */
export const experimentFormSchema = z
	.object({
		name: z.string().trim().min(1, msg("nameRequired")),
		suite: z.string().min(1, msg("suiteRequired")),
		base: z.string().min(1, msg("baseRequired")),
		armALabel: z.string().trim().min(1, msg("labelRequired")),
		armBLabel: z.string().trim().min(1, msg("labelRequired")),
		/** What arm B changes: a real control per gateway feature, or any config field by path (custom). */
		changeKind: z.enum(CHANGE_KINDS),
		overridePath: z.string().trim(),
		overrideValue: z.string().trim(),
		/** Comma-separated models: routing targets or failover fallbacks. */
		changeModels: z.string().trim(),
		routingStrategy: z.enum(["weighted", "fallback_chain", "fixed"]),
		cacheMode: z.enum(["auto_inject", "off"]),
		codeMode: z.boolean(),
		teamSize: z.number().int().min(2).max(4),
		/** Advanced spec options (U2 section 2). */
		alpha: z.number({ error: msg2("alphaRange") }).gt(0, msg2("alphaRange")).lt(1, msg2("alphaRange")),
		allowUncalibratedJudge: z.boolean(),
		heldOut: z.boolean(),
		aa: z.boolean(),
		fairness: z.boolean(),
		realism: z.enum(["", "neutral"]),
		egress: z.enum(["", "offline", "replay", "live-allowlisted"]),
		sandbagging: z.boolean(),
		attribution: z.boolean(),
		/** More spec blocks as a JSON object, merged into the spec; the server's plan validates them. */
		extraSpec: z.string().trim(),
		primaryCondition: z.string().min(1, msg("primaryRequired")),
		primaryMetric: z.string().min(1, msg("metricRequired")),
		conditions: z.array(conditionSchema),
		seed: z
			.number({ error: msg("seedInt") })
			.int(msg("seedInt"))
			.min(0, msg("seedInt")),
		concurrency: z
			.number({ error: msg("concurrencyRange") })
			.int(msg("concurrencyRange"))
			.min(1, msg("concurrencyRange"))
			.max(64, msg("concurrencyRange")),
		totalUsd: z.number({ error: msg("budgetPositive") }).positive(msg("budgetPositive")),
		// Hosted mode: the runner pool that runs the slots (schemas.RunnerSpec); empty takes the default pool.
		runnerPool: z.string().trim(),
	})
	.superRefine((v, ctx) => {
		if (v.changeKind === "custom") {
			if (!DOTTED_PATH.test(v.overridePath)) ctx.addIssue({ code: "custom", path: ["overridePath"], message: msg("pathFormat") });
			if (!v.overrideValue) ctx.addIssue({ code: "custom", path: ["overrideValue"], message: msg("valueRequired") });
		}
		if ((v.changeKind === "routing" || v.changeKind === "failover") && splitModels(v.changeModels).length === 0) {
			ctx.addIssue({ code: "custom", path: ["changeModels"], message: msg2("modelsRequired") });
		}
		if (v.extraSpec && parseExtraSpec(v.extraSpec) === null) {
			ctx.addIssue({ code: "custom", path: ["extraSpec"], message: msg2("specJson") });
		}
		if (v.armALabel.trim().toLowerCase() === v.armBLabel.trim().toLowerCase() && v.armBLabel.trim() !== "") {
			ctx.addIssue({ code: "custom", path: ["armBLabel"], message: msg("labelsDiffer") });
		}
		const enabled = v.conditions.filter((c) => c.enabled);
		if (enabled.length === 0) {
			ctx.addIssue({ code: "custom", path: ["conditions"], message: msg("conditionRequired") });
		}
		v.conditions.forEach((c, i) => {
			if (c.enabled && !c.allTasks && c.taskIds.length === 0) {
				ctx.addIssue({ code: "custom", path: ["conditions", i, "taskIds"], message: msg("tasksRequired") });
			}
		});
		if (v.primaryCondition && !enabled.some((c) => c.condition === v.primaryCondition)) {
			ctx.addIssue({ code: "custom", path: ["primaryCondition"], message: msg("primaryNotRun") });
		}
	});

export type ExperimentFormValues = z.infer<typeof experimentFormSchema>;
export type ConditionFormValues = ExperimentFormValues["conditions"][number];

/** "<id>@<version>", the suite reference an experiment names (DESIGN.md 16.6). */
export function suiteRef(suite: CatalogSuite): string {
	return `${suite.id}@${suite.version}`;
}

/**
 * clean plus every probe of the suite. Only clean starts on, running all tasks three times: a failure
 * test is a choice the user makes (N39), so every probe is listed and starts off.
 */
export function conditionsFor(suite: CatalogSuite | undefined): ConditionFormValues[] {
	if (!suite) return [];
	return ["clean", ...(suite.probes ?? [])].map((condition, i) => {
		const on = i === 0;
		return { condition, enabled: on, allTasks: true, taskIds: [], trials: on ? 3 : 1 };
	});
}

/**
 * Form defaults from the catalog, chosen like the builder's (N39): the benchmark with the most ready
 * tasks, the ?agent= agent or the first agent My agents lists, and the normal run as the main test.
 * The seed is passed in so this stays pure.
 */
export function defaultFormValues(catalog: Catalog, seed: number, opts: BuilderDefaultsOptions = {}): ExperimentFormValues {
	// A list the server sent as null (no agents/ folder) reads as empty here, never as a crash.
	const suite = catalog.suites ? defaultRunnableSuite(catalog, opts.ready) : undefined;
	const metrics = catalog.metrics ?? [];
	const primaryMetric = metrics.find((m) => m.name === "verified_success") ?? metrics[0];
	return {
		name: "",
		suite: suite ? suiteRef(suite) : "",
		base: (catalog.agents ? startAgent(catalog, opts) : undefined)?.path ?? "",
		armALabel: "baseline",
		armBLabel: "",
		changeKind: "custom",
		overridePath: "",
		overrideValue: "",
		changeModels: "",
		routingStrategy: "weighted",
		cacheMode: "off",
		codeMode: true,
		teamSize: 2,
		alpha: 0.05,
		allowUncalibratedJudge: false,
		heldOut: false,
		aa: false,
		fairness: false,
		realism: "",
		egress: "",
		sandbagging: false,
		attribution: false,
		extraSpec: "",
		primaryCondition: "clean",
		primaryMetric: primaryMetric?.name ?? "",
		conditions: conditionsFor(suite),
		seed,
		concurrency: 4,
		totalUsd: 25,
		runnerPool: "",
	};
}

/** Reads the override value as JSON when it parses (60, true, null, [..], {..}), otherwise as text. */
export function parseOverrideValue(raw: string): JsonValue {
	const text = raw.trim();
	try {
		return JSON.parse(text) as JsonValue;
	} catch {
		return text;
	}
}

/**
 * The experiment the form describes (DESIGN.md 16.6). Guards follow the design's default: no
 * regression on clean when clean runs and is not the primary, and no new integrity violation. With
 * ready tasks known, "all tasks" on a benchmark with tasks that are not ready sends the ready ids (N42).
 */
export function toExperiment(v: ExperimentFormValues, ready?: ReadyTasks): Experiment {
	const entry = ready?.get(v.suite);
	const allTasks = entry && entry.ids.length < entry.total ? { ids: [...entry.ids] } : { all: true };
	const enabled = v.conditions.filter((c) => c.enabled);
	const cleanGuard = enabled.some((c) => c.condition === "clean") && v.primaryCondition !== "clean";
	return {
		id: "",
		name: v.name.trim(),
		suite: v.suite,
		base: v.base,
		arms: {
			A: { label: v.armALabel.trim() },
			B: { label: v.armBLabel.trim(), set: armBSet(v) },
		},
		primary: { condition: v.primaryCondition, metric: v.primaryMetric },
		guards: [...(cleanGuard ? [{ condition: "clean", rule: "no_regression" }] : []), { rule: "no_new_integrity_violation" }],
		conditions: enabled.map((c) => ({
			id: c.condition,
			tasks: c.allTasks ? { ...allTasks } : { ids: [...c.taskIds] },
			trials: c.trials,
			...(v.heldOut ? { split: "held_out" as const } : {}),
		})),
		budget: { total_usd: v.totalUsd },
		verdict: { policy: "paired_signflip", alpha: v.alpha, assumed_sd: 0.3, ...(v.allowUncalibratedJudge ? { allow_uncalibrated_judge: true } : {}) },
		seed: v.seed,
		concurrency: v.concurrency,
		manifest_mode: "agent_exposure",
		...(v.runnerPool.trim() ? { runner: { pool: v.runnerPool.trim() } } : {}),
		...advancedSpec(v),
	};
}

/** Today as YYYYMMDD, the seed style of the design's example experiment. */
export function dateSeed(now: Date): number {
	return now.getFullYear() * 10_000 + (now.getMonth() + 1) * 100 + now.getDate();
}
/**
 * Whether a plan no longer describes the form. Both sides are compared as the experiment the
 * form submits, so the schema's own normalisation (trimmed name and labels) never marks a fresh
 * plan stale; values that no longer validate always do.
 */
export function planIsStale(planned: Experiment, values: unknown, ready?: ReadyTasks): boolean {
	const parsed = experimentFormSchema.safeParse(values);
	return !parsed.success || JSON.stringify(toExperiment(parsed.data, ready)) !== JSON.stringify(planned);
}