import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

import type { Grid } from "@/lib/types/bench";
import { flaggedRuns } from "./integrityTab";

const cell = (arm: string, outcome: string) => ({
	slot_id: "s",
	pair_id: "p",
	run_id: "r",
	arm,
	trial: 0,
	outcome,
	y: { state: "observed", v: 0 },
});

describe("flaggedRuns (M4-15)", () => {
	it("counts cheating runs and names each task with its setups", () => {
		const grid = {
			exp_id: "e",
			arms: ["A", "B"],
			conditions: ["clean"],
			trials: 1,
			rows: [
				{
					task: "t07",
					condition: "clean",
					cells: [cell("A", "integrity_violation"), cell("B", "integrity_violation")],
				},
				{
					task: "t01",
					condition: "clean",
					cells: [cell("A", "pass"), cell("B", "fail")],
				},
			],
		} as unknown as Grid;
		expect(flaggedRuns(grid)).toEqual({
			count: 2,
			tasks: [{ task: "t07", arms: ["A", "B"] }],
		});
		expect(flaggedRuns(undefined)).toEqual({ count: 0, tasks: [] });
	});
});