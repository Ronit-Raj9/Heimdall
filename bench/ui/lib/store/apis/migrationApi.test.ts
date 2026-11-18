import { describe, expect, it } from "vitest";
import { experimentPath, withParams } from "./migrationApi";

describe("migration requests", () => {
	it("escapes the experiment id as one path segment", () => {
		expect(experimentPath("e_pilot_mixed_nc93")).toBe("/experiments/e_pilot_mixed_nc93");
		expect(experimentPath("../x?y")).toBe("/experiments/..%2Fx%3Fy");
	});

	it("sends only the params that have a value", () => {
		expect(withParams("/experiments/e_1/migration", {})).toBe("/experiments/e_1/migration");
		expect(withParams("/experiments/e_1/migration", { from: "gpt-5.4-mini", to: "", condition: undefined })).toEqual({
			url: "/experiments/e_1/migration",
			params: { from: "gpt-5.4-mini" },
		});
		expect(withParams("/leaderboard/pareto", { x: "cost_per_run", k: 3 })).toEqual({
			url: "/leaderboard/pareto",
			params: { x: "cost_per_run", k: "3" },
		});
	});
});