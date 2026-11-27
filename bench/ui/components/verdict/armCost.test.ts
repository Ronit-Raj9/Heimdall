import { describe, expect, it } from "vitest";
import { cheaperArm, formatUsd2, roundSig2 } from "./armCost";

describe("armCost (M4-1, M4-2)", () => {
	it("formats money with two significant digits", () => {
		expect(formatUsd2(0.104, "en")).toBe("$0.10");
		expect(formatUsd2(0.0065, "en")).toBe("$0.0065");
		expect(formatUsd2(7.17, "en")).toBe("$7.2");
	});
	it("rounds a ratio to two significant digits", () => {
		expect(roundSig2(14.6)).toBe(15);
		expect(roundSig2(2.04)).toBe(2);
	});
	it("names the setup that costs 2 times less or more", () => {
		expect(cheaperArm(["A", "B"], { A: 0.095, B: 0.0065 })).toEqual({
			arm: "B",
			ratio: 15,
		});
		expect(cheaperArm(["A", "B"], { A: 0.01, B: 0.03 })).toEqual({
			arm: "A",
			ratio: 3,
		});
	});
	it("says nothing under 2 times, or with a missing or zero cost", () => {
		expect(cheaperArm(["A", "B"], { A: 0.1, B: 0.06 })).toBeNull();
		expect(cheaperArm(["A", "B"], { A: 0.1, B: null })).toBeNull();
		expect(cheaperArm(["A", "B"], { A: 0, B: 0.1 })).toBeNull();
		expect(cheaperArm(["A", "B", "C"], { A: 1, B: 0.1, C: 1 })).toBeNull();
	});
});