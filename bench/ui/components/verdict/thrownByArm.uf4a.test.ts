import { describe, expect, it } from "vitest";
import { thrownByArm } from "./verdictSummary";

// M4-11: runs thrown out for cheating per setup, from the grid.
describe("thrownByArm", () => {
	it("counts integrity violations per arm", () => {
		const rows = [
			{
				cells: [
					{ arm: "A", outcome: "integrity_violation" },
					{ arm: "B", outcome: "pass" },
				],
			},
			{
				cells: [
					{ arm: "A", outcome: "fail" },
					{ arm: "B", outcome: "integrity_violation" },
					{ arm: "B", outcome: null },
				],
			},
		];
		expect(thrownByArm(rows)).toEqual({ A: 1, B: 1 });
		expect(thrownByArm(undefined)).toEqual({});
	});
});