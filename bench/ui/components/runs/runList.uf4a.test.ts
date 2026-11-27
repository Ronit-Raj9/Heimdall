import { describe, expect, it } from "vitest";
import { byTaskThenSetup, filterHasChoice, passFailOnly } from "./runList";
import type { RunListRow } from "@/lib/types/bench";

const row = (task: string, arm: string, y: number | null, trial = 1) =>
	({ task, arm, trial, y: { state: y === null ? "unknown" : "observed", v: y } }) as unknown as RunListRow;

// Lane 4a phase B: the comparison's Runs tab (M4-19, M4-21, M4-22).
describe("run list helpers", () => {
	it("hides the Score column only for a pass or fail benchmark (M4-21)", () => {
		expect(passFailOnly([row("t1", "A", 1), row("t1", "B", 0), row("t2", "A", null)])).toBe(true);
		expect(passFailOnly([row("t1", "A", 0.5)])).toBe(false);
		expect(passFailOnly([row("t1", "A", null)])).toBe(false);
	});
	it("puts the setups of one task together, tasks in natural order (M4-22)", () => {
		const sorted = byTaskThenSetup([
			row("t10", "A", 1),
			row("t2", "B", 1),
			row("t2", "A", 0),
			row("t10", "B", 0, 2),
			row("t10", "B", 0, 1),
		]);
		expect(sorted.map((r) => `${r.task}${r.arm}${r.trial}`)).toEqual(["t2A1", "t2B1", "t10A1", "t10B1", "t10B2"]);
	});
	it("keeps a filter only when it offers a choice (M4-19)", () => {
		expect(filterHasChoice(["clean"], null)).toBe(false);
		expect(filterHasChoice(["clean"], "clean")).toBe(true);
		expect(filterHasChoice(["A", "B"], null)).toBe(true);
	});
});