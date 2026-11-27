import { describe, expect, it } from "vitest";
import { defaultExperimentTab, hiddenExperimentTabs, redirectExperimentTab, type ExperimentFacts } from "./experimentTabs";

const base: ExperimentFacts = {
	arms: 2,
	adaptive: false,
	scorecards: false,
	probes: false,
};

describe("hiddenExperimentTabs", () => {
	it("hides what a plain two-arm clean experiment lacks", () => {
		expect([...hiddenExperimentTabs(base)].sort()).toEqual(["adaptive", "export", "matrix", "receipts", "scorecards"]);
	});
	it("shows the matrix from three arms, and the rest when they apply", () => {
		// Extra trials and Export are never in the strip (M4-15).
		expect(
			[
				...hiddenExperimentTabs({
					arms: 3,
					adaptive: true,
					scorecards: true,
					probes: true,
				}),
			].sort(),
		).toEqual(["adaptive", "export"]);
	});
	it("hides migration with fewer than two arms", () => {
		expect(hiddenExperimentTabs({ ...base, arms: 1 }).has("migration")).toBe(true);
	});
	it("never hides verdict, runs, cost or integrity", () => {
		const hidden = hiddenExperimentTabs({
			arms: 0,
			adaptive: false,
			scorecards: false,
			probes: false,
		});
		for (const tab of ["verdict", "runs", "cost", "integrity"] as const) expect(hidden.has(tab)).toBe(false);
	});
});
describe("defaultExperimentTab (M4-51, was QC2-29-02)", () => {
	it("opens every experiment on its Result", () => {
		expect(defaultExperimentTab(3)).toBe("verdict");
		expect(defaultExperimentTab(2)).toBe("verdict");
		expect(defaultExperimentTab(0)).toBe("verdict");
	});
});
describe("redirectExperimentTab (M4-15)", () => {
	it("sends Extra trials to Actions and Export to the Result", () => {
		expect(redirectExperimentTab("adaptive")).toBe("actions");
		expect(redirectExperimentTab("export")).toBe("verdict");
		expect(redirectExperimentTab("cost")).toBeNull();
		expect(redirectExperimentTab(null)).toBeNull();
	});
});