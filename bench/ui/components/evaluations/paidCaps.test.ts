import { describe, expect, it } from "vitest";
import { cheatCheckBody, costPerRun, estimateOf, evaluationCostPerRun, rerunFailuresBody, suggestedBudget } from "./paidCaps";

describe("suggestedBudget", () => {
	// Round 3 suggested $5 for a 3 task run that cost cents (PH1-4); the same rule as the server's SuggestBudget.
	it("is about 3 times the estimate, at least $0.25, rounded up", () => {
		expect(suggestedBudget(0)).toBe(0.25);
		expect(suggestedBudget(0.0016)).toBe(0.25);
		expect(suggestedBudget(0.16)).toBe(0.5);
		expect(suggestedBudget(0.4)).toBe(1.25);
		expect(suggestedBudget(2.1)).toBe(6.5);
		expect(suggestedBudget(24)).toBe(72);
		expect(suggestedBudget(33.3)).toBe(100);
	});
	it("suggests nothing when the estimate is not known", () => {
		expect(suggestedBudget(null)).toBeNull();
		expect(suggestedBudget(undefined)).toBeNull();
		expect(suggestedBudget(Number.NaN)).toBeNull();
	});
});

describe("estimates", () => {
	it("uses the cost of one run, and is unknown when any arm's cost is", () => {
		expect(costPerRun({ A: { cost_usd: 0.003, runs: 3 } as never, B: { cost_usd: 0.001, runs: 1 } as never })).toBe(0.001);
		expect(costPerRun({ A: { cost_usd: null, runs: 3 } as never })).toBeNull();
		expect(estimateOf(4, 0.001)).toBe(0.004);
		expect(estimateOf(4, null)).toBeNull();
		expect(evaluationCostPerRun({ result: { arms: [{ runs: 2 }, { runs: 2 }], cost_usd: 0.02 } })).toBe(0.005);
		expect(evaluationCostPerRun({ result: { arms: [{ runs: 2 }] }, children: [{ spent_usd: 0.01 }, {}] })).toBeNull();
	});
});

describe("request bodies", () => {
	// The Go tests post these exact bodies to the handlers (cheatcheck_w448_test.go, evaluationrerun_uff5_test.go).
	it("sends the cap the person set", () => {
		expect(JSON.stringify(cheatCheckBody(0.5))).toBe('{"yes":true,"budget_usd":0.5}');
		expect(JSON.stringify(rerunFailuresBody(0.3))).toBe('{"budget_usd":0.3}');
	});
});
