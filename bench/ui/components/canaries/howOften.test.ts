import { describe, expect, it } from "vitest";
import { checkCost, howOftenCron, howOftenOf, howOftenProblem, newHowOften } from "./howOften";

describe("howOften (M7-47, M7-48)", () => {
	it("turns presets into cron lines and back", () => {
		expect(howOftenCron(newHowOften({ when: "daily", time: "02:05" }))).toBe("5 2 * * *");
		expect(howOftenCron(newHowOften({ when: "weekly", time: "06:00", weekday: 0 }))).toBe("0 6 * * 0");
		expect(howOftenCron(newHowOften({ when: "hourly" }))).toBe("0 * * * *");
		expect(howOftenCron(newHowOften({ when: "manual" }))).toBeNull();
		expect(howOftenOf("5 2 * * *", "Europe/Berlin")).toMatchObject({ when: "daily", time: "02:05", timezone: "Europe/Berlin" });
		expect(howOftenOf("0 6 * * 7", "")).toMatchObject({ when: "weekly", weekday: 0, timezone: "UTC" });
		expect(howOftenOf("*/10 * * * *", "UTC")).toMatchObject({ when: "custom", cron: "*/10 * * * *" });
	});
	it("checks only the custom line", () => {
		expect(howOftenProblem(newHowOften({ when: "custom", cron: "61 25 * *" }))).toBe("invalid");
		expect(howOftenProblem(newHowOften({ when: "custom", cron: "" }))).toBe("empty");
		expect(howOftenProblem(newHowOften({ when: "daily" }))).toBeNull();
	});
});

describe("checkCost (M7-50)", () => {
	it("hourly with $1 a run and $3 a day is at most $90 a month", () => {
		expect(checkCost(24, 1, 3)).toEqual({ runs: 3, perRun: 1, day: 3, month: 90 });
	});
	it("a weekly check costs about four runs a month", () => {
		expect(checkCost(1 / 7, 1, 3)?.month).toBeCloseTo(30 / 7);
	});
	it("is null with no runs or a missing limit", () => {
		expect(checkCost(0, 1, 3)).toBeNull();
		expect(checkCost(24, 0, 3)).toBeNull();
	});
});
