import { describe, expect, it } from "vitest";
import type { ExperimentPlan } from "@/lib/types/bench";
import { confirmText, overBudget, planMinutes, suiteReady } from "./featureTestDialog";

const plan = (usd: number | null, wall: number | null) =>
	({
		errors: [],
		est_usd: usd === null ? { state: "unknown", v: null } : { state: "observed", v: usd },
		est_wall_s: wall === null ? { state: "unknown", v: null } : { state: "observed", v: wall },
	}) as unknown as ExperimentPlan;

describe("feature test estimate against the budget (Q-295, Q-288)", () => {
	it("says when the estimate is above the budget, in the button's format", () => {
		expect(overBudget(15, 5)).toEqual({ est: "$15", budget: "$5.00" });
		expect(overBudget(0.12, 5)).toBeNull();
		expect(overBudget(null, 5)).toBeNull();
	});

	it("keeps the minutes when only the cost is unknown", () => {
		expect(planMinutes(plan(null, 600))).toBe(10);
		expect(planMinutes(plan(null, null))).toBeNull();
	});

	it("confirm amounts use the same format as the button", () => {
		const t = (k: string, o: { est?: string; budget: string }) => `${k}|${o.est ?? ""}|${o.budget}`;
		expect(confirmText(t, plan(15, 60), 5)).toBe("dialog.confirm|15|5.00");
	});
});

describe("benchmark readiness in the picker (Q-298)", () => {
	it("marks a benchmark with tasks not ready", () => {
		expect(suiteReady({ task_count: 89, admitted_count: 24 })).toBe(false);
		expect(suiteReady({ task_count: 89, admitted_count: 89 })).toBe(true);
		expect(suiteReady({ task_count: 10 })).toBe(true);
	});
});
