import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { CHANGE_KINDS, experimentFormSchema, toExperiment, type ExperimentFormValues } from "./experimentFormSchema";

// The body the New comparison form sends, per arm B change kind. The same file is posted through the
// server's experiment schema and agent override resolver in handlers/experiments_ufl2_test.go.
const fixture = JSON.parse(readFileSync(resolve(__dirname, "../../../../../handlers/testdata/ufl2_web_experiment.json"), "utf8")) as {
	arms: Record<string, Record<string, unknown>>;
	advanced: Record<string, unknown>;
};

const base: ExperimentFormValues = experimentFormSchema.parse({
	name: "demo",
	suite: "s@1",
	base: "agents/a.yaml",
	armALabel: "baseline",
	armBLabel: "changed",
	overridePath: "model",
	overrideValue: "claude-other",
	primaryCondition: "clean",
	primaryMetric: "verified_success",
	conditions: [{ condition: "clean", enabled: true, allTasks: true, taskIds: [], trials: 3 }],
	seed: 1,
	concurrency: 4,
	totalUsd: 5,
	runnerPool: "",
	changeKind: "custom",
	changeModels: "gpt-4o-mini, claude-haiku-4-5",
	routingStrategy: "weighted",
	cacheMode: "auto_inject",
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
});

describe("arm B change controls (round 5 L2)", () => {
	it.each(CHANGE_KINDS)("%s sends the set the server resolves", (kind) => {
		const exp = toExperiment({ ...base, changeKind: kind });
		expect(exp.arms.B.set).toEqual(fixture.arms[kind]);
	});

	it("routing strategies other than weighted", () => {
		expect(toExperiment({ ...base, changeKind: "routing", routingStrategy: "fallback_chain" }).arms.B.set).toEqual(
			fixture.arms.routing_fallback_chain,
		);
	});

	it("advanced options land as spec blocks and the extra JSON merges on top", () => {
		const v = experimentFormSchema.parse({
			...base,
			alpha: 0.01,
			allowUncalibratedJudge: true,
			heldOut: true,
			aa: true,
			fairness: true,
			realism: "neutral",
			egress: "offline",
			sandbagging: true,
			attribution: true,
			extraSpec: '{"safety": "trifecta"}',
		});
		const exp = toExperiment(v) as unknown as Record<string, unknown>;
		for (const [k, want] of Object.entries(fixture.advanced)) expect(exp[k]).toEqual(want);
	});

	it("refuses what the server would refuse: no models, a bad JSON box, a bad significance", () => {
		const bad = experimentFormSchema.safeParse({ ...base, changeKind: "failover", changeModels: " ", extraSpec: "[1]", alpha: 1 });
		expect(bad.success).toBe(false);
		const paths = bad.error?.issues.map((i) => i.path.join(".")) ?? [];
		expect(paths).toEqual(expect.arrayContaining(["changeModels", "extraSpec", "alpha"]));
	});

	it("a real control does not need the raw path", () => {
		expect(experimentFormSchema.safeParse({ ...base, changeKind: "prompt_cache", overridePath: "", overrideValue: "" }).success).toBe(true);
	});
});