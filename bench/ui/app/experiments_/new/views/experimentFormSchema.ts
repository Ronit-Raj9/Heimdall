import type benchEn from "@/locales/en/bench.json";
import type { Catalog, CatalogSuite, Experiment, JsonValue } from "@/lib/types/bench";
import { z } from "zod";

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

/** clean plus every probe of the suite. clean and the first probe run all tasks three times; the rest start off. */
export function conditionsFor(suite: CatalogSuite | undefined): ConditionFormValues[] {
	if (!suite) return [];
	return ["clean", ...(suite.probes ?? [])].map((condition, i) => {
		const on = i <= 1;
		return { condition, enabled: on, allTasks: true, taskIds: [], trials: on ? 3 : 1 };
	});
}

/** Form defaults from the catalog. The seed is passed in so this stays pure. */
export function defaultFormValues(catalog: Catalog, seed: number): ExperimentFormValues {
	// A list the server sent as null (no agents/ folder) reads as empty here, never as a crash.
	const suite = catalog.suites?.[0];
	const metrics = catalog.metrics ?? [];
	const primaryMetric = metrics.find((m) => m.name === "verified_success") ?? metrics[0];
	return {
		name: "",
		suite: suite ? suiteRef(suite) : "",
		base: catalog.agents?.[0]?.path ?? "",
		armALabel: "baseline",
		armBLabel: "",
		overridePath: "",
		overrideValue: "",
		primaryCondition: suite?.probes?.[0] ?? "clean",
		primaryMetric: primaryMetric?.name ?? "",
		conditions: conditionsFor(suite),
		seed,
		concurrency: 4,
		totalUsd: 25,
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
 * regression on clean when clean runs and is not the primary, and no new integrity violation.
 */
export function toExperiment(v: ExperimentFormValues): Experiment {
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
			tasks: c.allTasks ? { all: true } : { ids: [...c.taskIds] },
			trials: c.trials,
		})),
		budget: { total_usd: v.totalUsd },
		verdict: { policy: "paired_signflip", alpha: 0.05, assumed_sd: 0.3 },
		seed: v.seed,
		concurrency: v.concurrency,
		manifest_mode: "agent_exposure",
	};
}

/** Today as YYYYMMDD, the seed style of the design's example experiment. */
export function dateSeed(now: Date): number {
	return now.getFullYear() * 10_000 + (now.getMonth() + 1) * 100 + now.getDate();
}