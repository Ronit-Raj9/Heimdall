import { describe, expect, it } from "vitest";
import { cronProblem, formatRun, nextRuns, runsPerDay } from "./cron";

describe("cronProblem (M7-46)", () => {
	it("refuses text that is not a five-field cron line", () => {
		expect(cronProblem("every night lol")).toBe("invalid");
		expect(cronProblem("61 25 * *")).toBe("invalid");
		expect(cronProblem("61 2 * * *")).toBe("invalid");
		expect(cronProblem("0 24 * * *")).toBe("invalid");
		expect(cronProblem("0 2 0 * *")).toBe("invalid");
		expect(cronProblem("0 2 * 13 *")).toBe("invalid");
		expect(cronProblem("0 2 * * 8")).toBe("invalid");
		expect(cronProblem("*/0 * * * *")).toBe("invalid");
		expect(cronProblem("  ")).toBe("empty");
	});
	it("accepts lists, ranges and steps", () => {
		for (const ok of ["0 2 * * *", "*/15 * * * *", "0 9-17 * * 1-5", "0,30 6 1,15 * *", "0 6 * * 7"]) expect(cronProblem(ok)).toBeNull();
	});
});

describe("nextRuns", () => {
	const from = new Date("2026-11-27T12:00:00Z");
	it("lists the next times in the zone", () => {
		const runs = nextRuns("0 2 * * *", "Europe/Berlin", 2, from);
		expect(runs.map((d) => d.toISOString())).toEqual(["2026-11-28T01:00:00.000Z", "2026-11-29T01:00:00.000Z"]);
		expect(formatRun(runs[0], "Europe/Berlin")).toBe("Sat 28 Nov 02:00");
		expect(formatRun(runs[0], "UTC")).toBe("Sat 28 Nov 01:00");
	});
	it("handles weekdays and half-hour zones", () => {
		expect(nextRuns("0 6 * * 1", "UTC", 1, from)[0].toISOString()).toBe("2026-11-30T06:00:00.000Z");
		expect(nextRuns("0 2 * * *", "Asia/Kolkata", 1, from)[0].toISOString()).toBe("2026-11-27T20:30:00.000Z");
	});
	it("gives nothing for a bad line", () => {
		expect(nextRuns("nope", "UTC", 2, from)).toEqual([]);
	});
	it("counts runs per day", () => {
		expect(runsPerDay("0 * * * *", from)).toBe(24);
		expect(runsPerDay("0 2 * * *", from)).toBe(1);
		expect(runsPerDay("0 6 * * 1", from)).toBeCloseTo(1 / 7);
	});
});
