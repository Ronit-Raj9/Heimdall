import { describe, expect, it } from "vitest";
import type { CostApart, Value } from "@/lib/types/bench";
import { measuredApart } from "./costWaterfall";

const v = (x: number | null, bounds?: [number, number]): Value => ({ state: x === null ? "unknown" : "observed", v: x, ...(bounds ? { bounds } : {}) }) as Value;
const row = (id: string, a: Value, b: Value): CostApart => ({ id, label: id, a, b, diff: v(null) });

describe("measuredApart (M4-24)", () => {
	it("drops measuring costs that are zero or unknown for both setups", () => {
		const rows = [row("judge", v(0), v(0)), row("simuser", v(null), v(0)), row("judge2", v(0), v(0.4)), row("bounded", v(null, [0, 1]), v(0))];
		expect(measuredApart(rows).map((r) => r.id)).toEqual(["judge2", "bounded"]);
		expect(measuredApart([])).toEqual([]);
	});
});
