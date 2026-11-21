import { describe, expect, it } from "vitest";
import { defaultExperimentTab, hiddenExperimentTabs, type ExperimentFacts } from "./experimentTabs";

const base: ExperimentFacts = { arms: 2, adaptive: false, scorecards: false, probes: false };

describe("hiddenExperimentTabs", () => {
	it("hides what a plain two-arm clean experiment lacks", () => {
		expect([...hiddenExperimentTabs(base)].sort()).toEqual(["adaptive", "matrix", "receipts", "scorecards"]);
	});
	it("shows the matrix from three arms, and the rest when they apply", () => {
		expect([...hiddenExperimentTabs({ arms: 3, adaptive: true, scorecards: true, probes: true })]).toEqual([]);
	});
	it("hides migration with fewer than two arms", () => {
		expect(hiddenExperimentTabs({ ...base, arms: 1 }).has("migration")).toBe(true);
	});
	it("never hides verdict, runs, cost, integrity or export", () => {
		const hidden = hiddenExperimentTabs({ arms: 0, adaptive: false, scorecards: false, probes: false });
		for (const tab of ["verdict", "runs", "cost", "integrity", "export"] as const) expect(hidden.has(tab)).toBe(false);
	});
});
describe("defaultExperimentTab (QC2-29-02)", () => {
	it("opens a three-arm experiment on its matrix and a two-arm one on its verdict", () => {
		expect(defaultExperimentTab(3)).toBe("matrix");
		expect(defaultExperimentTab(2)).toBe("verdict");
		expect(defaultExperimentTab(0)).toBe("verdict");
	});
});
