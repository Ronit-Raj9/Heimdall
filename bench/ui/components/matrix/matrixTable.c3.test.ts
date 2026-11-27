import { describe, expect, it } from "vitest";
import type { MatrixRow } from "@/lib/types/bench";
import { notMeasuredCount, rankMatrixRows } from "./matrixTable";

const row = (arm: string, rank: number, tier: number, cost: number | null, trials = 3) =>
	({
		arm,
		rank,
		tier,
		trials,
		p_vs_leader: rank === 1 ? null : 0.5,
		metrics: { cost_per_success: cost === null ? { state: "unknown", reason: "x" } : { state: "observed", v: cost } },
	}) as unknown as MatrixRow;

describe("rankMatrixRows (M4-48)", () => {
	it("prints =1 for every tie and lists ties cheapest first", () => {
		const out = rankMatrixRows([row("A", 1, 1, 0.5), row("B", 2, 1, 0.1), row("C", 3, 1, null), row("D", 4, 2, 0.01)]);
		expect(out.map((r) => [r.row.arm, r.rank])).toEqual([
			["B", "=1"],
			["A", "=1"],
			["C", "=1"],
			["D", "4"],
		]);
	});
	it("keeps plain ranks with one leader", () => {
		expect(rankMatrixRows([row("A", 1, 1, 0.5), row("B", 2, 2, 0.1)]).map((r) => r.rank)).toEqual(["1", "2"]);
	});
});

describe("notMeasuredCount (M4-54)", () => {
	it("counts setups with fewer than 2 tries", () => {
		expect(notMeasuredCount([{ trials: 1 }, { trials: 2 }, { trials: 0 }])).toBe(2);
	});
});
