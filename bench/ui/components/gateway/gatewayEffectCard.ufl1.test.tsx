import { describe, expect, it } from "vitest";
import type { GwArmResult } from "@/lib/types/gateway";
import { diffRows, noVerdictReason } from "./gatewayEffectCard";

const arm = (runs: number, solve?: number) => ({ arm: "A", label: "a", runs, tasks: 1, solve_rate: solve }) as GwArmResult;

describe("effect differences (round 5 L1)", () => {
	it("shows difference and range per measure", () => {
		expect(diffRows({ solve_delta: { point: -0.05, low: -0.12, high: 0.02 }, cost_delta: { point: 0.0012 } })).toEqual([
			{ key: "solve", diff: "-5 pts", range: "-12 pts to +2 pts" },
			{ key: "cost", diff: "+$0.0012", range: "" },
		]);
	});
	it("gives no verdict under 10 runs or when nothing is solved", () => {
		expect(noVerdictReason({ arms: [arm(9, 0.5), arm(12, 0.5)] })).toBe("fewRuns");
		expect(noVerdictReason({ arms: [arm(10, 0), arm(10)] })).toBe("noneSolved");
		expect(noVerdictReason({ arms: [arm(10, 0.2), arm(10, 0)] })).toBeNull();
	});
});