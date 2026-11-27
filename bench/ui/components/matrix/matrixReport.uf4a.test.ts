import { describe, expect, it } from "vitest";
import { anyPairBelow } from "./matrixReport";

// M4-50: the pairwise table opens by itself only when a pair differs.
describe("anyPairBelow", () => {
	it("is true only for an adjusted p below alpha", () => {
		expect(anyPairBelow([{ p_adjusted: 1 }, { p_adjusted: null }], 0.05)).toBe(false);
		expect(anyPairBelow([{ p_adjusted: 0.01 }], 0.05)).toBe(true);
		expect(anyPairBelow([{ p_adjusted: 0.01 }], undefined)).toBe(false);
	});
});