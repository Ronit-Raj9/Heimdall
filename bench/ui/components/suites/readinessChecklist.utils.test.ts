import { describe, expect, it } from "vitest";
import { checklistSteps, nextStep } from "./readinessChecklist.utils";

const states = (s: Parameters<typeof checklistSteps>[0]) => checklistSteps(s).map((step) => step.state);

describe("checklistSteps", () => {
	it("a new benchmark has every step to do, starting with Check files", () => {
		const steps = checklistSteps({ lock_state: "unlocked", admitted_count: 0, task_count: 3 });
		expect(steps.map((s) => s.state)).toEqual(["todo", "todo", "todo"]);
		expect(nextStep(steps)).toBe("lint");
	});
	it("a frozen benchmark with no ready task points at the readiness check", () => {
		const steps = checklistSteps({ lock_state: "locked", admitted_count: 0, task_count: 3 });
		expect(steps.map((s) => s.state)).toEqual(["done", "done", "todo"]);
		expect(nextStep(steps)).toBe("admit");
	});
	it("changed files ask for a new freeze; some ready is partly", () => {
		expect(states({ lock_state: "stale", admitted_count: 1, task_count: 3 })).toEqual(["done", "redo", "partly"]);
	});
	it("all ready is done", () => {
		const steps = checklistSteps({ lock_state: "locked", admitted_count: 3, task_count: 3 });
		expect(nextStep(steps)).toBeNull();
	});
});
