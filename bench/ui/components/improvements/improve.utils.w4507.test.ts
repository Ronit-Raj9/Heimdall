import { describe, expect, it } from "vitest";
import { agentIdOf, badgeKey, detectablePoints, money, pointsValue, versionNumber } from "./improve.utils";

describe("improve utils (wave 4)", () => {
	it("formats points, money and the smallest detectable gain", () => {
		expect(pointsValue(23.94)).toBe("24");
		expect(pointsValue(4.25)).toBe("4.3");
		expect(money(6.4)).toBe("6.40");
		expect(detectablePoints(0.239, 18)).toBe("24");
		expect(detectablePoints(0, 1)).toBeUndefined();
		expect(detectablePoints(0.2, 1)).toBeUndefined();
	});

	it("reads refs and states", () => {
		expect(versionNumber("agents/x.yaml@v3")).toBe(3);
		expect(agentIdOf("agents/coder.yaml@v2")).toBe("coder");
		expect(badgeKey("likely_better")).toBe("likely_better");
		expect(badgeKey("inconclusive")).toBeUndefined();
	});
});