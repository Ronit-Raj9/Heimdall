import type { ExperimentStatus } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { clampCap, defaultInvestigation, findingExperiment, investigableExperiments, rerunEstimate } from "./findingPlans";

const exp = (id: string, failed: number, finished: string, spent: number | null = null, done = 0) =>
	({
		exp_id: id,
		name: id,
		slots: { total: done, done, running: 0, failed },
		finished_at: finished,
		spent_usd: spent === null ? { state: "unknown", v: null } : { state: "observed", v: spent },
	}) as unknown as ExperimentStatus;

describe("clampCap", () => {
	it("holds the cap between 0.1 and 5", () => {
		expect(clampCap("50")).toBe(5);
		expect(clampCap("0")).toBe(0.1);
		expect(clampCap("abc")).toBe(0.1);
		expect(clampCap(2.5)).toBe(2.5);
	});
});

describe("investigation default", () => {
	it("puts comparisons with failed runs first, newest first, and opens on the newest of them", () => {
		const list = [
			exp("old-fail", 2, "2026-11-01T00:00:00Z"),
			exp("new-ok", 0, "2026-11-20T00:00:00Z"),
			exp("new-fail", 1, "2026-11-10T00:00:00Z"),
		];
		expect(investigableExperiments(list).map((e) => e.exp_id)).toEqual(["new-fail", "old-fail", "new-ok"]);
		expect(defaultInvestigation(list)).toBe("new-fail");
		expect(defaultInvestigation([exp("ok", 0, "2026-11-01T00:00:00Z")])).toBe("");
	});
});

describe("fix rerun", () => {
	it("takes the newest comparison of the history", () => {
		const history = [
			{ at: "", by: "", from: "", to: "", reason: "", experiment: "e1" },
			{ at: "", by: "", from: "", to: "", reason: "" },
			{ at: "", by: "", from: "", to: "", reason: "", experiment: "e2" },
			{ at: "", by: "", from: "", to: "", reason: "" },
		];
		expect(findingExperiment({ history })).toBe("e2");
		expect(findingExperiment({ history: [] })).toBe("");
	});
	it("prices the runs at the source comparison's cost per run", () => {
		expect(rerunEstimate(3, 2, exp("e", 0, "", 1.0, 10))).toEqual({
			runs: 12,
			usd: 1.2,
		});
		expect(rerunEstimate(3, 2, exp("e", 0, "", null, 10))).toEqual({
			runs: 12,
			usd: null,
		});
		expect(rerunEstimate(3, 2)).toEqual({ runs: 12, usd: null });
	});
});