import "@/lib/i18n";

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { applyPatch, draftFromLink, draftToSpec, EMPTY_DRAFT, isEvaluationMode, specToPatch } from "./evaluateDraft";
import { LEARN_GOALS } from "./step2Learn";
import { addModelArm, advancedChanged, removeModelArm, specProblem } from "./step4Setup";

// The body handlers/evaluations_l3wizard_test.go posts to POST /evaluations.
const FIXTURE = JSON.parse(readFileSync(fileURLToPath(new URL("../../../../handlers/testdata/l3_wizard_model_switch.json", import.meta.url)), "utf8"));

describe("Find the best model (mode model_switch)", () => {
	it("is a step 2 goal and a link mode", () => {
		expect(LEARN_GOALS).toContain("model_switch");
		expect(isEvaluationMode("model_switch")).toBe(true);
		expect(draftFromLink({ agent: "scripted", mode: "model_switch" }).badMode).toBeUndefined();
	});

	it("builds the body the server test posts, through the wizard's own steps", () => {
		let d = applyPatch(EMPTY_DRAFT, { agent: "scripted", arms: [] });
		d = applyPatch(d, { mode: "model_switch" });
		expect(d.arms.map((a) => a.label)).toEqual(["A", "B", "C"]);
		// Step 4: pick a model per row, the benchmark (step 3), the held-out split.
		let spec = draftToSpec(d);
		spec = { ...spec, arms: spec.arms.map((a, i) => ({ ...a, model: `m${i + 1}` })), benchmarks: ["longmemeval"], tasks_per_benchmark: 1 };
		expect(specProblem(spec)).toBe("");
		spec = { ...spec, split: "held_out" };
		expect(advancedChanged(spec)).toBe(true);
		d = applyPatch(d, specToPatch(spec, d));
		expect(draftToSpec(d)).toEqual(FIXTURE);
	});

	it("adds and removes model rows within 3 to 6 and refuses a repeated model", () => {
		const d = applyPatch(applyPatch(EMPTY_DRAFT, { agent: "scripted", arms: [] }), { mode: "model_switch" });
		let spec = { ...draftToSpec(d), benchmarks: ["b"] };
		for (let i = 0; i < 5; i++) spec = addModelArm(spec);
		expect(spec.arms.length).toBe(6);
		spec = removeModelArm(spec, 1);
		expect(spec.arms.map((a) => a.label)).toEqual(["A", "B", "C", "D", "E"]);
		const three = { ...spec, arms: spec.arms.slice(0, 3) };
		expect(removeModelArm(three, 2).arms.length).toBe(3);
		const dup = { ...three, arms: three.arms.map((a) => ({ ...a, model: "same" })) };
		expect(specProblem(dup)).toBe("Each model can be picked once.");
	});

	it("sends no split for public tasks", () => {
		expect("split" in draftToSpec({ ...EMPTY_DRAFT, agent: "x" })).toBe(false);
	});
});
