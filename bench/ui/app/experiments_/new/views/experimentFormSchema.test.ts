import type { Catalog } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import {
	conditionsFor,
	dateSeed,
	defaultFormValues,
	experimentFormSchema,
	parseOverrideValue,
	toExperiment,
	type ExperimentFormValues,
} from "./experimentFormSchema";

const CATALOG: Catalog = {
	mode: "synthetic",
	suites: [{ id: "coding-core", version: "1.0.0", tasks: ["t01", "t02", "t03", "t04"], probes: ["stale-notes", "model-outage"] }],
	probes: [],
	agents: [{ id: "claude-code", path: "agents/claude-code.yaml", adapter: "command", model: "claude-haiku-4-5" }],
	metrics: [
		{
			name: "steps",
			version: "1",
			label: "Steps",
			kind: "count",
			higher_is_better: false,
			min_evidence: "gateway",
			description: "",
			ps2: true,
		},
		{
			name: "verified_success",
			version: "1",
			label: "Verified success",
			kind: "rate",
			higher_is_better: true,
			min_evidence: "outcome",
			description: "",
			ps2: true,
		},
	],
	gates: [],
	parts: {},
};

function valid(): ExperimentFormValues {
	return {
		...defaultFormValues(CATALOG, 20261114),
		name: "code-wins sentence",
		armBLabel: "code-wins sentence",
		overridePath: "notes.user_file",
		overrideValue: "fixtures/notes/codewins.md",
	};
}

function issues(values: ExperimentFormValues): Record<string, string> {
	const result = experimentFormSchema.safeParse(values);
	if (result.success) return {};
	return Object.fromEntries(result.error.issues.map((issue) => [issue.path.join("."), issue.message]));
}

describe("defaultFormValues", () => {
	it("starts from the catalog: first suite and agent, clean and the first probe on, verified success primary", () => {
		const d = defaultFormValues(CATALOG, 7);
		expect(d.suite).toBe("coding-core@1.0.0");
		expect(d.base).toBe("agents/claude-code.yaml");
		expect(d.primaryCondition).toBe("stale-notes");
		expect(d.primaryMetric).toBe("verified_success");
		expect(d.conditions.map((c) => [c.condition, c.enabled, c.trials])).toEqual([
			["clean", true, 3],
			["stale-notes", true, 3],
			["model-outage", false, 1],
		]);
		expect(d.seed).toBe(7);
	});
});

describe("experimentFormSchema", () => {
	const cases: { name: string; change: (v: ExperimentFormValues) => ExperimentFormValues; path: string; message: string }[] = [
		{ name: "name", change: (v) => ({ ...v, name: "  " }), path: "name", message: "experiments.new.validation.nameRequired" },
		{
			name: "dotted path",
			change: (v) => ({ ...v, overridePath: "notes..file" }),
			path: "overridePath",
			message: "experiments.new.validation.pathFormat",
		},
		{
			name: "value",
			change: (v) => ({ ...v, overrideValue: "" }),
			path: "overrideValue",
			message: "experiments.new.validation.valueRequired",
		},
		{
			name: "same labels",
			change: (v) => ({ ...v, armBLabel: "Baseline" }),
			path: "armBLabel",
			message: "experiments.new.validation.labelsDiffer",
		},
		{
			name: "no condition",
			change: (v) => ({ ...v, conditions: v.conditions.map((c) => ({ ...c, enabled: false })) }),
			path: "conditions",
			message: "experiments.new.validation.conditionRequired",
		},
		{
			name: "primary not run",
			change: (v) => ({ ...v, primaryCondition: "model-outage" }),
			path: "primaryCondition",
			message: "experiments.new.validation.primaryNotRun",
		},
		{
			name: "chosen tasks empty",
			change: (v) => ({ ...v, conditions: v.conditions.map((c, i) => (i === 1 ? { ...c, allTasks: false, taskIds: [] } : c)) }),
			path: "conditions.1.taskIds",
			message: "experiments.new.validation.tasksRequired",
		},
		{
			name: "trials range",
			change: (v) => ({ ...v, conditions: v.conditions.map((c, i) => (i === 0 ? { ...c, trials: 0 } : c)) }),
			path: "conditions.0.trials",
			message: "experiments.new.validation.trialsRange",
		},
		{ name: "seed NaN", change: (v) => ({ ...v, seed: Number.NaN }), path: "seed", message: "experiments.new.validation.seedInt" },
		{
			name: "concurrency",
			change: (v) => ({ ...v, concurrency: 65 }),
			path: "concurrency",
			message: "experiments.new.validation.concurrencyRange",
		},
		{ name: "budget", change: (v) => ({ ...v, totalUsd: 0 }), path: "totalUsd", message: "experiments.new.validation.budgetPositive" },
	];

	it("accepts a valid form", () => {
		expect(issues(valid())).toEqual({});
	});
	for (const c of cases) {
		it(`rejects ${c.name}`, () => {
			expect(issues(c.change(valid()))[c.path]).toBe(c.message);
		});
	}
});

describe("toExperiment", () => {
	it("builds the experiment of DESIGN.md 16.6 from the form", () => {
		const values = valid();
		values.conditions = values.conditions.map((c) =>
			c.condition === "model-outage" ? { ...c, enabled: true, allTasks: false, taskIds: ["t01", "t04"], trials: 2 } : c,
		);
		const exp = toExperiment(values);
		expect(exp.arms).toEqual({
			A: { label: "baseline" },
			B: { label: "code-wins sentence", set: { "notes.user_file": "fixtures/notes/codewins.md" } },
		});
		expect(exp.primary).toEqual({ condition: "stale-notes", metric: "verified_success" });
		expect(exp.guards).toEqual([{ condition: "clean", rule: "no_regression" }, { rule: "no_new_integrity_violation" }]);
		expect(exp.conditions).toEqual([
			{ id: "clean", tasks: { all: true }, trials: 3 },
			{ id: "stale-notes", tasks: { all: true }, trials: 3 },
			{ id: "model-outage", tasks: { ids: ["t01", "t04"] }, trials: 2 },
		]);
		expect(exp.budget).toEqual({ total_usd: 25 });
		expect(exp.verdict).toEqual({ policy: "paired_signflip", alpha: 0.05, assumed_sd: 0.3 });
		expect(exp.suite).toBe("coding-core@1.0.0");
		expect(exp.id).toBe("");
	});

	it("drops the clean guard when clean is the primary", () => {
		expect(toExperiment({ ...valid(), primaryCondition: "clean" }).guards).toEqual([{ rule: "no_new_integrity_violation" }]);
	});
});

describe("parseOverrideValue", () => {
	const cases: [string, unknown][] = [
		["fixtures/notes/codewins.md", "fixtures/notes/codewins.md"],
		["60", 60],
		["true", true],
		['"quoted"', "quoted"],
		['["a"]', ["a"]],
		["claude-sonnet-5-5", "claude-sonnet-5-5"],
	];
	for (const [raw, want] of cases) {
		it(raw, () => expect(parseOverrideValue(raw)).toEqual(want));
	}
});

describe("conditionsFor and dateSeed", () => {
	it("has no conditions without a suite", () => expect(conditionsFor(undefined)).toEqual([]));
	it("formats the date as YYYYMMDD", () => expect(dateSeed(new Date(2026, 10, 14))).toBe(20261114));
});

// U1 found it: a bench root without an agents/ folder sent agents: null and the form crashed here.
describe("defaultFormValues with null lists", () => {
	it("reads null agents, suites, probes and metrics as empty", () => {
		const bare = {
			mode: "live",
			suites: [{ id: "s", version: "1", tasks: null, probes: null }],
			probes: null,
			agents: null,
			metrics: null,
			gates: null,
			parts: {},
		};
		const values = defaultFormValues(bare as unknown as Catalog, 7);
		expect(values.base).toBe("");
		expect(values.suite).toBe("s@1");
		expect(values.primaryCondition).toBe("clean");
		expect(values.conditions.map((c) => c.condition)).toEqual(["clean"]);
		expect(defaultFormValues({ suites: null, agents: null, metrics: null } as unknown as Catalog, 7).suite).toBe("");
	});
});