import { describe, expect, it } from "vitest";
import { firstWithFinishedRuns, latestRunAgent } from "./patternDefaults";

describe("pattern defaults", () => {
	it("opens on the first comparison with finished runs", () => {
		const options = [{ id: "e3" }, { id: "e2" }, { id: "e1" }];
		const exps = [
			{ exp_id: "e3", slots: { done: 0 } },
			{ exp_id: "e2", slots: { done: 4 } },
		];
		expect(firstWithFinishedRuns(options, exps)).toBe("e2");
		expect(firstWithFinishedRuns(options, [])).toBe("e3");
		expect(firstWithFinishedRuns([], [])).toBeUndefined();
	});
	it("picks the agent that ran last", () => {
		expect(
			latestRunAgent([
				{ id: "a", last_run_at: "2026-11-01T00:00:00Z" },
				{ id: "b", last_run_at: "2026-11-20T00:00:00Z" },
				{ id: "c", last_run_at: null },
			]),
		).toBe("b");
		expect(latestRunAgent([{ id: "c" }])).toBeUndefined();
	});
});