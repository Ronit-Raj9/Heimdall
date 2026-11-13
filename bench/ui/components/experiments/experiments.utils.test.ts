import type { ExperimentStatus } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { newestExperiment, readWriteToken, saveWriteToken } from "./experiments.utils";

function status(exp_id: string, frozen_at: string, started_at?: string): ExperimentStatus {
	return {
		exp_id,
		name: exp_id,
		state: "done",
		manifest_hash: "h",
		frozen_at,
		started_at,
		slots: { total: 1, done: 1, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
	};
}

describe("newestExperiment", () => {
	it("picks the latest start, else the latest freeze", () => {
		const list = [
			status("e_old", "2026-11-10T00:00:00Z", "2026-11-10T01:00:00Z"),
			status("e_new", "2026-11-12T00:00:00Z"),
			status("e_mid", "2026-11-11T00:00:00Z", "2026-11-11T05:00:00Z"),
		];
		expect(newestExperiment(list)?.exp_id).toBe("e_new");
	});
	it("is null for an empty list", () => {
		expect(newestExperiment([])).toBeNull();
	});
});

describe("write token storage", () => {
	it("is a no-op without a browser", () => {
		saveWriteToken("secret");
		expect(readWriteToken()).toBe("");
	});
});