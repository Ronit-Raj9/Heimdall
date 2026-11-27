import i18n from "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { extendTexts, suiteGone } from "./actionsTab";
import { cheapestSetup } from "./matrixTab";

const t = i18n.getFixedT("en", "ufrc") as unknown as (k: string, o?: Record<string, unknown>) => string;

// Round 2 lane RC: the Extend label carries the amount and the confirm prints no second rounding of it (Q-33).
describe("extendTexts", () => {
	it("puts the price in the label with the same format as the estimate line", () => {
		const { label, confirm } = extendTexts(t, "tasks", 65, "tasks", { runs: 130, cost: 7.17 });
		expect(label).toBe("Extend by 65 tasks, about $7.17");
		expect(confirm).not.toMatch(/\$/);
		expect(confirm).toContain("130 runs");
		expect(confirm).toContain("which caps spend");
	});
	it("leaves the price out of the label when it is not known", () => {
		expect(extendTexts(t, "trials", 2, "tries", { runs: 32, cost: null }).label).toBe("Extend by 2 more tries");
	});
});

describe("suiteGone (Q-20)", () => {
	it("is true only for a 404", () => {
		expect(suiteGone({ status: 404 })).toBe(true);
		expect(suiteGone({ status: 500 })).toBe(false);
		expect(suiteGone(undefined)).toBe(false);
	});
});

describe("cheapestSetup (Q-253)", () => {
	it("picks the lowest known median cost", () => {
		const v = (n?: number) => (n === undefined ? { state: "unknown" as const } : { state: "observed" as const, v: n });
		const rows = [
			{ arm: "A", metrics: { cost_usd: v(0.2) } },
			{ arm: "B", metrics: { cost_usd: v() } },
			{ arm: "C", metrics: { cost_usd: v(0.05) } },
		];
		expect(cheapestSetup(rows as never)).toEqual({ arm: "C", cost: 0.05 });
		expect(cheapestSetup([])).toBeNull();
	});
});