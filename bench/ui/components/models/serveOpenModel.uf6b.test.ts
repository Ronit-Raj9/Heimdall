import { describe, expect, it } from "vitest";
import { hoursProblem } from "./serveOpenModel";

describe("serve time limit (M8-15)", () => {
	it("refuses 0 and anything over 24 hours", () => {
		expect(hoursProblem(0)).toBe(true);
		expect(hoursProblem(Number.NaN)).toBe(true);
		expect(hoursProblem(25)).toBe(true);
		expect(hoursProblem(0.1)).toBe(false);
		expect(hoursProblem(24)).toBe(false);
	});
});
