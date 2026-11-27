import { describe, expect, it } from "vitest";
import { retentionShrinks } from "./dataControls";

describe("retentionShrinks (M8-55)", () => {
	it("asks when the new retention deletes data the old one keeps", () => {
		expect(retentionShrinks(90, 30)).toBe(true);
		expect(retentionShrinks(0, 365)).toBe(true); // forever to a limit
	});
	it("does not ask for a longer or unlimited retention", () => {
		expect(retentionShrinks(30, 90)).toBe(false);
		expect(retentionShrinks(30, 0)).toBe(false);
		expect(retentionShrinks(30, 30)).toBe(false);
	});
});