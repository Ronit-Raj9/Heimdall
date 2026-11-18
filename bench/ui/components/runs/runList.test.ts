import { describe, expect, it } from "vitest";
import { RUN_LIST_PAGE, formatShortTime, runListRequest } from "./runList";

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