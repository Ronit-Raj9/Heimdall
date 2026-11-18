import { describe, expect, it } from "vitest";
import type { CostPart, Pareto, ParetoPoint, Value } from "@/lib/types/bench";
import { costText, formatUsd, niceMax, paretoModel, signedUsd, waterfallBars } from "./pareto.utils";

const obs = (v: number, bounds?: [number, number]): Value => ({ state: "observed", v, ...(bounds ? { bounds } : {}) });
const point = (id: string, x: Value, y: Value, frontier: boolean, placed = true): ParetoPoint => ({
	id,
	label: id,
	agent: "agent",
	model: id,
	x,
	y,
	placed,
	frontier,
	dominated_by: frontier ? [] : ["A"],
	reason: placed ? "" : "cost unknown: cost unknown in 1 of 3 runs",
});
const plane = (points: ParetoPoint[]): Pareto => ({
	x: "cost_per_success",
	x_label: "Cost per verified success",
	y: "verified_success",
	points,
	frontier: [],
	note: "",
});

describe("niceMax", () => {
	it("rounds up to 1, 2 or 5 times a power of ten", () => {
		expect(niceMax(0.031)).toBeCloseTo(0.05);
		expect(niceMax(0.2)).toBeCloseTo(0.2);
		expect(niceMax(3.2)).toBe(5);
		expect(niceMax(0)).toBe(1);
	});
});

describe("paretoModel", () => {
	const model = paretoModel(
		plane([
			point("A", obs(0.1), obs(0.9, [0.8, 1]), true),
			point("B", obs(0.05), obs(0.5), true),
			point("C", obs(0.2), obs(0.4), false),
			point("D", { state: "unknown", v: null, reason: "cost unknown" }, obs(1), false, false),
		]),
	);

	it("never draws an unplaced point, at zero or anywhere", () => {
		expect(model.placed.map((p) => p.point.id)).toEqual(["A", "B", "C"]);
		expect(model.unplaced.map((p) => p.id)).toEqual(["D"]);
	});

	it("joins the frontier cheapest first and keeps the success interval", () => {
		const b = model.placed.find((p) => p.point.id === "B")!;
		const a = model.placed.find((p) => p.point.id === "A")!;
		expect(model.frontierPath.startsWith(`M${Math.round(b.x * 10) / 10},`)).toBe(true);
		expect(a.interval).not.toBeNull();
		expect(a.interval![1]).toBeLessThan(a.interval![0]); // higher success is higher on the page
		expect(a.y).toBeLessThan(b.y);
	});

	it("puts more cost further right", () => {
		const xs = Object.fromEntries(model.placed.map((p) => [p.point.id, p.x]));
		expect(xs.B).toBeLessThan(xs.A);
		expect(xs.A).toBeLessThan(xs.C);
	});
});

const part = (id: CostPart["id"], usd: Value): CostPart => ({ id, label: id, usd, detail: "" });

describe("waterfallBars", () => {
	it("moves a running total and scales to the widest position", () => {
		const bars = waterfallBars([part("price", obs(0.02)), part("input_tokens", obs(-0.01)), part("turns", obs(0.03))]);
		expect(bars.map((b) => [b.start, b.end])).toEqual([
			[0, 0.5],
			[0.5, 0.25],
			[0.25, 1],
		]);
		expect(bars.every((b) => b.known)).toBe(true);
	});

	it("draws an unknown part as its bounds from the running total, flagged unknown", () => {
		const bars = waterfallBars([part("price", obs(0.1)), part("unpriced", { state: "unknown", v: null, bounds: [0, 0.1] })]);
		expect(bars[1]).toMatchObject({ known: false, start: 0.5, end: 1 });
	});
});

describe("money", () => {
	it("keeps small costs readable and signs differences", () => {
		expect(formatUsd(0.00412)).toBe("$0.00412");
		expect(formatUsd(0.0321)).toBe("$0.032");
		expect(formatUsd(1.25)).toBe("$1.25");
		expect(signedUsd(0.012)).toBe("+$0.012");
		expect(signedUsd(-0.004)).toBe("-$0.00400");
		expect(costText({ state: "unknown", v: null, bounds: [0.01, 0.02] })).toBe("$0.010 to $0.020");
		expect(costText({ state: "unknown", v: null })).toBeNull();
	});
});