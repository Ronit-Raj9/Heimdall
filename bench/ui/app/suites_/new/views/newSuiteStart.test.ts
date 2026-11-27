import { describe, expect, it } from "vitest";
import { startFromUrl } from "./newSuiteStart";

describe("startFromUrl", () => {
	it("reads ?start=", () => {
		expect(startFromUrl("code", null)).toBe("code");
		expect(startFromUrl("other", null)).toBe("other");
	});
	it("maps ?template= support, qa and commits to the same choices", () => {
		expect(startFromUrl(null, "support")).toBe("questions");
		expect(startFromUrl(null, "qa")).toBe("questions");
		expect(startFromUrl(null, "commits")).toBe("code");
	});
	it("lets start win when both are given", () => {
		expect(startFromUrl("code", "support")).toBe("code");
		expect(startFromUrl("questions", "commits")).toBe("questions");
	});
	it("falls back to questions for unknown or missing values", () => {
		expect(startFromUrl(null, null)).toBe("questions");
		expect(startFromUrl("nope", "nope")).toBe("questions");
	});
});
