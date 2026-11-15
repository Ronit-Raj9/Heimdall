import { describe, expect, it } from "vitest";
import type { TrendLineage } from "@/lib/types/bench";
import { trendsFixture } from "./trends.fixture";
import {
	ALL_VALUE,
	DEFAULT_FRAME,
	SERIES_SLOTS,
	chartModel,
	formatMetric,
	isReleaseFlagged,
	latestValuePoint,
	lineageLabel,
	linePath,
	niceMax,
	seriesSlots,
	shortHash,
	slotColor,
	totalPoints,
} from "./trends.utils";

const data = trendsFixture();

describe("formatMetric", () => {
	const cases = [
		{ v: 0.75, metric: "verified_success", want: "75%" },
		{ v: 0.034, metric: "false_completion", want: "3.4%" },
		{ v: 48210, metric: "tokens", want: "48.2K" },
		{ v: 312, metric: "tokens", want: "312" },
	] as const;
	for (const c of cases) {
		it(`${c.metric} ${c.v}`, () => expect(formatMetric(c.v, c.metric, "en")).toBe(c.want));
	}
	it("returns null for a value that was not observed, never zero", () => {
		expect(formatMetric(null, "cost_per_success", "en")).toBeNull();
		expect(formatMetric(Number.NaN, "tokens", "en")).toBeNull();
	});
	it("formats cost per success in dollars", () => expect(formatMetric(0.21, "cost_per_success", "en")).toContain("$"));
});

describe("identity helpers", () => {
	it("shortens a config hash and drops the sha256 prefix", () => {
		expect(shortHash("sha256:895fa8468a3bc0d6f1e2")).toBe("895fa8468a3b");
		expect(shortHash("abc")).toBe("abc");
	});
	it("names a lineage by agent and model", () => {
		expect(lineageLabel({ agent: "codex", model: "gpt-5.4-mini" })).toBe("codex (gpt-5.4-mini)");
		expect(lineageLabel({ agent: "codex", model: "" })).toBe("codex");
	});
	it("flags only regressed or blocked releases", () => {
		expect(isReleaseFlagged({ release: "regressed" })).toBe(true);
		expect(isReleaseFlagged({ release: "blocked" })).toBe(true);
		expect(isReleaseFlagged({ release: "" })).toBe(false);
	});
	it("keeps the all sentinel out of real ids", () => expect(ALL_VALUE).toMatch(/^~/));
});

describe("seriesSlots", () => {
	it("follows the agent's place in the unfiltered agent list, not the lineage's rank", () => {
		const slots = seriesSlots(data.lineages, data.agents);
		expect(slots.get("claude-code-gpt|openai/gpt-5.1")).toBe(0);
		expect(slots.get("codex|gpt-5.4-mini")).toBe(1);
		// Filtering to codex alone keeps its color.
		const codexOnly = data.lineages.filter((l) => l.agent === "codex");
		expect(seriesSlots(codexOnly, data.agents).get("codex|gpt-5.4-mini")).toBe(1);
	});
	it("gives a second model of one agent the next free slot", () => {
		const a = { ...data.lineages[1], key: "codex|m1", model: "m1" };
		const b = { ...data.lineages[1], key: "codex|m2", model: "m2" };
		const slots = seriesSlots([a, b], ["codex", "zeta"]);
		expect(new Set([slots.get("codex|m1"), slots.get("codex|m2")])).toEqual(new Set([0, 1]));
	});
	it("folds slots past the palette into the other color", () => {
		expect(slotColor(0)).toBe("var(--color-chart-cat-1)");
		expect(slotColor(SERIES_SLOTS)).toBe("var(--color-chart-cat-other)");
	});
});

describe("chartModel", () => {
	const slots = seriesSlots(data.lineages, data.agents);
	const model = chartModel(data.lineages, "verified_success", slots);

	it("fixes rates to a 0 to 1 axis", () => {
		expect(model.yMax).toBe(1);
		expect(model.yTicks[0]).toMatchObject({ value: 0, pos: DEFAULT_FRAME.height - DEFAULT_FRAME.bottom });
		expect(model.yTicks.at(-1)).toMatchObject({ value: 1, pos: DEFAULT_FRAME.top });
	});

	it("places time left to right and keeps the success interval", () => {
		const codex = model.series.find((s) => s.lineage.agent === "codex")!;
		const xs = codex.points.map((p) => p.x);
		expect(xs[0]).toBe(DEFAULT_FRAME.left);
		expect(xs.at(-1)).toBe(DEFAULT_FRAME.width - DEFAULT_FRAME.right);
		expect([...xs].sort((a, b) => a - b)).toEqual(xs);
		const [lo, hi] = codex.points[0].interval!;
		expect(lo).toBeGreaterThan(hi); // svg y grows downward
	});

	it("starts magnitudes at zero with a nice maximum", () => {
		const tokens = data.lineages.map((l) => ({ ...l, points: l.points.map((p) => ({ ...p, value: p.tokens })) }));
		const m = chartModel(tokens, "tokens", slots);
		expect(m.yMax).toBe(100_000);
		expect(m.series[0].points[0].interval).toBeNull();
	});

	it("pads a single date so the point is not on the edge", () => {
		const one: TrendLineage[] = [{ ...data.lineages[0] }];
		const m = chartModel(one, "verified_success", slots);
		expect(m.series[0].points[0].x).toBeCloseTo(DEFAULT_FRAME.left + (DEFAULT_FRAME.width - DEFAULT_FRAME.left - DEFAULT_FRAME.right) / 2);
	});
});

describe("linePath", () => {
	it("breaks the line at a point with no value instead of bridging it", () => {
		expect(
			linePath([
				{ x: 1, y: 2 },
				{ x: 3, y: null },
				{ x: 5, y: 6 },
				{ x: 7, y: 8 },
			]),
		).toBe("M1,2M5,6L7,8");
		expect(linePath([])).toBe("");
	});
});

describe("niceMax", () => {
	const cases = [
		{ max: 0.34, want: 0.5 },
		{ max: 61300, want: 100_000 },
		{ max: 401, want: 500 },
		{ max: 1, want: 1 },
		{ max: 0, want: 1 },
	];
	for (const c of cases) it(`${c.max}`, () => expect(niceMax(c.max)).toBe(c.want));
});

describe("totals and latest", () => {
	it("counts points", () => {
		expect(totalPoints(data)).toBe(5);
		expect(totalPoints({ ...data, points: undefined as unknown as number })).toBe(5);
	});
	it("finds the newest point with a value", () => {
		const codex = data.lineages.find((l) => l.agent === "codex")!;
		expect(latestValuePoint(codex)?.experiment).toBe("e_synthetic-providers");
		const blank = { ...codex, points: codex.points.map((p) => ({ ...p, value: null })) };
		expect(latestValuePoint(blank)).toBeNull();
	});
});