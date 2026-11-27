import { describe, expect, it } from "vitest";
import { hasFinishedEvaluation, homeUsageRange, monthStartUTC, setupSteps } from "./home.utils";

describe("Home set-up card and spend range (fix wave lane 2a)", () => {
	it("ticks each set-up step from what the org has (M2-10)", () => {
		const steps = setupSteps({ keys: true, agent: false, evaluation: false, finished: false });
		expect(steps.map((s) => s.id)).toEqual(["keys", "agent", "evaluate", "report"]);
		expect(steps.map((s) => s.done)).toEqual([true, false, false, false]);
		expect(steps[0]).toMatchObject({ to: "/settings", search: { tab: "keys" } });
		expect(steps[1].to).toBe("/agents/new");
	});

	it("hides the card once an evaluation has finished", () => {
		expect(hasFinishedEvaluation([{ status: "running" }, { status: "failed" }])).toBe(false);
		expect(hasFinishedEvaluation([{ status: "done" }])).toBe(true);
	});

	it("reads Spent over this calendar month in UTC (M8-19, N8-4)", () => {
		const now = new Date("2026-11-27T23:30:00-05:00");
		expect(monthStartUTC(now).toISOString()).toBe("2026-11-01T00:00:00.000Z");
		expect(homeUsageRange(now)).toEqual({ from: "2026-11-01", to: "2026-11-28" });
	});
});
