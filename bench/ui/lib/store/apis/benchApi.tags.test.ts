import { describe, expect, it } from "vitest";
import { experimentResultTags, pairOpenable, reviewInvalidatedTags } from "./benchApi";

describe("experimentResultTags (6-C-02)", () => {
	it("refreshes every result slice of the experiment that finished", () => {
		const types = experimentResultTags("e1").map((tag) => `${tag.type}:${tag.id}`);
		expect(types).toEqual(
			expect.arrayContaining(["BenchVerdict:e1", "BenchDiagnostics:e1", "BenchGrid:e1", "BenchRun:LIST"]),
		);
	});
	it("refreshes the migration, cost, Pareto and extension summaries too (QC2-27-02)", () => {
		const types = experimentResultTags("e1").map((tag) => `${tag.type}:${tag.id}`);
		expect(types).toEqual(
			expect.arrayContaining(["BenchMigration:e1", "BenchCostExplain:e1", "BenchPareto:e1", "BenchExperimentResult:e1"]),
		);
	});
});

describe("reviewInvalidatedTags (6-C-04)", () => {
	it("refreshes diagnostics together with the reviewed verdict", () => {
		const tags = reviewInvalidatedTags("e1", "r1");
		expect(tags).toContainEqual({ type: "BenchVerdict", id: "e1" });
		expect(tags).toContainEqual({ type: "BenchDiagnostics", id: "e1" });
	});
	it("refreshes the run's reviews without an experiment", () => {
		expect(reviewInvalidatedTags(undefined, "r1")).toEqual([{ type: "BenchReviews", id: "run:r1" }, "BenchLeaderboard"]);
	});
});

describe("pairOpenable (QC2-29-04)", () => {
	it("opens every pair against a server and only fixture-backed pairs in fixture mode", () => {
		expect(pairOpenable("p_anything", false)).toBe(true);
		expect(pairOpenable("p_ltosmymyqy", true)).toBe(true);
		expect(pairOpenable("p_oo2rekitzo", true)).toBe(false);
	});
});
