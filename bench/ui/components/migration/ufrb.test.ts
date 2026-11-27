import { describe, expect, it } from "vitest";
import { attemptsOf, setupHead, shownParts } from "./costWaterfall";
import { reportNotes } from "./migrationReport";
import { formatUsd } from "./pareto.utils";

describe("lane RB migration fixes", () => {
	it("names a setup with its model (Q-139)", () => {
		expect(setupHead({ arm: "A", label: "baseline", model: "gpt-5.6-terra" })).toBe("A (gpt-5.6-terra)");
	});
	it("drops zero rows and parts (Q-157)", () => {
		expect(attemptsOf({ retries_per_run: 0, fallbacks_per_run: 0, rejected_per_run: 0 })).toBe(0);
		const parts = [{ usd: { state: "observed", v: 0 } }, { usd: { state: "observed", v: 0.01 } }] as never[];
		expect(shownParts(parts)).toHaveLength(1);
	});
	it("prints $0.10, not $0.104 (Q-137)", () => {
		expect(formatUsd(0.104)).toBe("$0.10");
	});
	it("prints a caveat once (Q-178)", () => {
		const pairs = [{ recommendation: { caveats: ["Same prompt."] } }] as never[];
		expect(reportNotes({ notes: ["Same prompt.", "Other."], pairs })).toEqual(["Other."]);
	});
});

import { paretoModel } from "./pareto.utils";
import { meanCostText } from "./migrationReport";

describe("lane RB, lane RC requests", () => {
	it("keeps every point inside the x axis", () => {
		const v = (x: number) => ({ state: "observed", v: x }) as never;
		const m = paretoModel({ x_label: "cost", points: [
			{ id: "A", placed: true, x: v(0.05), y: v(0.5), frontier: true },
			{ id: "B", placed: true, x: v(0.119), y: v(0.7), frontier: true },
		] } as never);
		const right = m.frame.width - m.frame.right;
		for (const p of m.placed) expect(p.x).toBeLessThan(right);
	});
	it("prints the average cost per run as the Result tab does", () => {
		expect(meanCostText({ state: "observed", v: 0.104 } as never, "en")).toBe("$0.10");
	});
});
