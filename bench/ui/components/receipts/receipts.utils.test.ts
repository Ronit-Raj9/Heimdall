import { RECEIPT_STEPS } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { LAYER_STEPS, ratioShares, stepApplies } from "./receipts.utils";

describe("stepApplies", () => {
	it("follows each layer's declared steps (DESIGN.md 10.3)", () => {
		expect(LAYER_STEPS.gateway).toEqual(RECEIPT_STEPS);
		expect(stepApplies("fired", "fixture", undefined)).toBe(false);
		expect(stepApplies("visible", "fixture", undefined)).toBe(true);
		expect(stepApplies("fired", "tool_shim", undefined)).toBe(true);
		expect(stepApplies("delivered", "tool_shim", undefined)).toBe(false);
		expect(stepApplies("absorbed_by", "gateway", undefined)).toBe(true);
	});

	it("infers from the data when the layer is unknown", () => {
		const ratios = { A: { fired: { num: 1, den: 2, unknown: 0 } }, B: {} };
		expect(stepApplies("fired", null, ratios)).toBe(true);
		expect(stepApplies("delivered", null, ratios)).toBe(false);
	});
});

describe("ratioShares", () => {
	it("keeps unknowns as their own share", () => {
		expect(ratioShares({ num: 2, den: 12, unknown: 1 })).toEqual({ yes: 2 / 12, unknown: 1 / 12 });
	});
	it("has no shares without a denominator", () => {
		expect(ratioShares({ num: 0, den: 0, unknown: 0 })).toBeNull();
		expect(ratioShares(undefined)).toBeNull();
	});
	it("never overflows the bar", () => {
		expect(ratioShares({ num: 5, den: 4, unknown: 3 })).toEqual({ yes: 1, unknown: 0 });
	});
});