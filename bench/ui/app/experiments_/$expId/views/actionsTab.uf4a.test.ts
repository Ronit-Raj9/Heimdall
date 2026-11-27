import { describe, expect, it } from "vitest";
import type { MoreEvidence } from "@/lib/types/bench";
import { extendEstimate } from "./actionsTab";

const more: MoreEvidence = {
	target_pp: 10,
	tasks: 65,
	tasks_total: 73,
	sd: 0.4,
	sd_assumed: false,
	runs: 130,
	cost_usd: 7.17,
	cost_basis: "mean",
	combination: "inverse_normal",
};

describe("extendEstimate (M4-33)", () => {
	it("prices the suggested tasks from the sizing", () => {
		const e = extendEstimate("tasks", 65, more, 2);
		expect(e.runs).toBe(130);
		expect(e.cost).toBeCloseTo(7.17);
	});
	it("counts more tries as one run per task already run and per setup", () => {
		expect(extendEstimate("trials", 2, more, 2)).toEqual({
			runs: 32,
			cost: (32 * 7.17) / 130,
		});
	});
	it("says the price is not known without a cost", () => {
		expect(extendEstimate("tasks", 10, { ...more, cost_usd: undefined }, 2)).toEqual({ runs: 20, cost: null });
	});
	it("knows nothing without a sizing or past the cap", () => {
		expect(extendEstimate("tasks", 10, null, 2)).toEqual({
			runs: null,
			cost: null,
		});
		expect(extendEstimate("tasks", 10, { ...more, over: true }, 2)).toEqual({
			runs: null,
			cost: null,
		});
		expect(extendEstimate("trials", 2, more, undefined)).toEqual({
			runs: null,
			cost: null,
		});
	});
});