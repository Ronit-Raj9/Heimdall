import type benchEn from "@/locales/en/bench.json";
import type { Catalog, CatalogSuite, Experiment, JsonValue } from "@/lib/types/bench";
import { z } from "zod";
// A cycle with matrixBuilderSchema (it imports suiteRef): both only call each other's functions at run time.
import { defaultRunnableSuite, startAgent, type BuilderDefaultsOptions, type ReadyTasks } from "./matrixBuilderSchema";

/** A t() key for a validation message. Zod messages are keys, translated where the error renders. */
export type ValidationKey = `experiments.new.validation.${keyof (typeof benchEn)["experiments"]["new"]["validation"]}`;

const msg = (key: keyof (typeof benchEn)["experiments"]["new"]["validation"]): ValidationKey => `experiments.new.validation.${key}`;

/** Dotted agent-config path, for example notes.user_file or budgets.turns (DESIGN.md 8.6). */
const DOTTED_PATH = /^[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z0-9_-]+)*$/;

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
		overridePath: z.string().trim().regex(DOTTED_PATH, msg("pathFormat")),
		overrideValue: z.string().trim().min(1, msg("valueRequired")),
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
		overridePath: "",
		overrideValue: "",
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
			B: { label: v.armBLabel.trim(), set: { [v.overridePath.trim()]: parseOverrideValue(v.overrideValue) } },
		},
		primary: { condition: v.primaryCondition, metric: v.primaryMetric },
		guards: [...(cleanGuard ? [{ condition: "clean", rule: "no_regression" }] : []), { rule: "no_new_integrity_violation" }],
		conditions: enabled.map((c) => ({
			id: c.condition,
			tasks: c.allTasks ? { ...allTasks } : { ids: [...c.taskIds] },
			trials: c.trials,
		})),
		budget: { total_usd: v.totalUsd },
		verdict: { policy: "paired_signflip", alpha: 0.05, assumed_sd: 0.3 },
		seed: v.seed,
		concurrency: v.concurrency,
		manifest_mode: "agent_exposure",
		...(v.runnerPool.trim() ? { runner: { pool: v.runnerPool.trim() } } : {}),
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