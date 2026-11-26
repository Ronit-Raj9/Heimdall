import { describe, expect, it } from "vitest";
import { pastLastPage } from "./runsTable";

describe("pastLastPage", () => {
	it("names the last page when the page asked for is past the end", () => {
		expect(pastLastPage(324, 99, 50)).toBe(7);
	});
	it("is null when there are no runs at all or the page has rows", () => {
		expect(pastLastPage(0, 1, 50)).toBeNull();
		expect(pastLastPage(324, 7, 50)).toBeNull();
	});
});