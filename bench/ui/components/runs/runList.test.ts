import { describe, expect, it } from "vitest";
import { RUN_LIST_PAGE, faultText, formatShortTime, runListRequest } from "./runList";

describe("runListRequest", () => {
	it("leaves out empty filters and the first page's offset", () => {
		expect(runListRequest({ exp: "e_x", arm: null, task: "" }, null)).toEqual({ limit: RUN_LIST_PAGE, exp: "e_x" });
	});
	it("turns a page into an offset", () => {
		expect(runListRequest({ arm: "C", outcome: "fail" }, 3)).toEqual({
			limit: RUN_LIST_PAGE,
			arm: "C",
			outcome: "fail",
			offset: 2 * RUN_LIST_PAGE,
		});
	});
});

describe("formatShortTime", () => {
	it("formats a time and leaves a bad one empty", () => {
		expect(formatShortTime("2026-11-14T08:02:00Z", "en-US")).toMatch(/Nov 14/);
		expect(formatShortTime("not a date", "en-US")).toBe("");
		expect(formatShortTime(undefined, "en-US")).toBe("");
	});
});
describe("faultText (Q-255)", () => {
	it("joins the condition and the attempt with a comma and a space", () => {
		expect(faultText("clean", "attempt 1")).toBe("clean, attempt 1");
		expect(faultText("", "attempt 2")).toBe("attempt 2");
	});
});
