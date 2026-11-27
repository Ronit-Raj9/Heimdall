import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { effectText, naiveScore } from "./sharedView";

describe("shared comparison (M8-68)", () => {
	it("scores a setup from its runs", () => {
		expect(naiveScore({ num: 7, den: 8 })).toBe(0.875);
		expect(naiveScore({ num: 0, den: 0 })).toBeNull();
		expect(naiveScore(undefined)).toBeNull();
	});
	it("writes the effect with its range when there is one", () => {
		const fmt = (e: string, lo: string, hi: string) => `${e} [${lo}, ${hi}]`;
		expect(effectText(undefined, fmt)).toBe("-");
		const base = { condition: "", metric: "", arm_a: "A", arm_b: "B", n_tasks: 8, n_nonzero: 2, n_positive: 2, n_negative: 0, per_task: [], descriptive: false };
		expect(effectText({ ...base, d_bar: 0.125 }, fmt)).toBe("+13 pts");
		expect(effectText({ ...base, d_bar: 0.125, interval: [-0.05, 0.3] }, fmt)).toBe("+13 pts [-5 pts, +30 pts]");
	});
});
