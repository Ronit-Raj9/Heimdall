import { describe, expect, it } from "vitest";
import { formatUsd, formatUsdRatio } from "@/lib/money";
import { formatCompact, formatCount, formatDuration, formatShare, rangesOverlap, shareOf, wilson } from "@/lib/numbers";

describe("shares (Spec 12, M1-55)", () => {
	it("prints counts only under 10 runs", () => {
		expect(formatShare(7, 8)).toBe("7 of 8");
		expect(shareOf(7, 8).tooFew).toBe(true);
		expect(formatShare(3, 4, 5)).toBe("3 of 4");
	});
	it("prints a whole percent with counts from 10 to 29", () => {
		expect(formatShare(8, 13)).toBe("62% (8 of 13)");
		expect(shareOf(8, 13).range).toMatch(/^likely between \d+% and \d+%$/);
	});
	it("prints the percent and n from 30, one decimal only from 100", () => {
		expect(shareOf(22, 31)).toMatchObject({ text: "71%", sample: "n = 31" });
		expect(formatShare(625, 1000)).toBe("62.5%");
	});
	it("never prints a share of nothing", () => {
		expect(formatShare(0, 0)).toBe("-");
	});
	it("computes Wilson ranges inside 0..1 and calls overlaps tied", () => {
		const w = wilson(0, 10)!;
		expect(w.low).toBe(0);
		expect(w.high).toBeGreaterThan(0.2);
		expect(wilson(1, 0)).toBeNull();
		expect(rangesOverlap({ k: 6, n: 10 }, { k: 7, n: 10 })).toBe(true);
		expect(rangesOverlap({ k: 10, n: 100 }, { k: 90, n: 100 })).toBe(false);
	});
});

describe("durations and counts", () => {
	it("follows the duration rule", () => {
		expect(formatDuration(850)).toBe("850 ms");
		expect(formatDuration(6200)).toBe("6.2 s");
		expect(formatDuration(13000)).toBe("13 s");
		expect(formatDuration(252000)).toBe("4 min 12 s");
		expect(formatDuration(3900000)).toBe("1 h 5 min");
		expect(formatDuration(null)).toBe("-");
	});
	it("separates and compacts counts", () => {
		expect(formatCount(113351)).toBe("113,351");
		expect(formatCompact(84000)).toBe("84k");
		expect(formatCompact(1200000)).toBe("1.2M");
		expect(formatCompact(950)).toBe("950");
	});
});

describe("money", () => {
	it("keeps formatUsd and adds the ratio", () => {
		expect(formatUsd(0.004)).toBe("$0.0040");
		expect(formatUsdRatio(15, 1)).toBe("15x");
		expect(formatUsdRatio(3, 2)).toBe("1.5x");
		expect(formatUsdRatio(1, 0)).toBe("-");
	});
});
