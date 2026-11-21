import { EXPERIMENT_POLL_MS } from "@/lib/store/apis/benchApi";
import { describe, expect, it } from "vitest";
import { CREATED_POLL_MS, progressPollMs } from "./newExperimentView";

describe("progressPollMs (QC2-27-04)", () => {
	it("keeps watching a created experiment so a start from another tab or the CLI shows", () => {
		expect(progressPollMs("created")).toBe(CREATED_POLL_MS);
		expect(CREATED_POLL_MS).toBeGreaterThan(0);
	});
	it("polls live experiments at the live pace and stops once terminal", () => {
		expect(progressPollMs(undefined)).toBe(EXPERIMENT_POLL_MS);
		expect(progressPollMs("running")).toBe(EXPERIMENT_POLL_MS);
		for (const state of ["done", "failed", "cancelled"] as const) expect(progressPollMs(state)).toBe(0);
	});
});
