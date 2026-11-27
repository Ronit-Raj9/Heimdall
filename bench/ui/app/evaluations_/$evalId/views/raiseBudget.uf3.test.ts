import { describe, expect, it } from "vitest";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { budgetStopOf } from "./raiseBudget";

describe("budgetStopOf (M3-4)", () => {
	it("names the budget and the planned runs of a capped evaluation", () => {
		const capped = {
			id: "ev",
			status: "done",
			spec: { budget_usd: 0.01 },
			children: [{ benchmark: "b", experiment_id: "e", status: "done", budget_usd: 0.01, budget_stopped: true, not_started: 7 }],
		} as unknown as Evaluation;
		expect(budgetStopOf(capped, 3)).toEqual({ budget: expect.stringContaining("0.01"), done: 3, planned: 10 });
		expect(budgetStopOf({ ...capped, children: [] } as Evaluation, 3)).toBeUndefined();
	});
});
