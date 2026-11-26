import type { ExperimentStatus } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { newestExperiment } from "./experiments.utils";

function status(exp_id: string, started_at: string): ExperimentStatus {
	return {
		exp_id,
		name: exp_id,
		state: "done",
		manifest_hash: "h",
		frozen_at: started_at,
		started_at,
		slots: { total: 1, done: 1, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
	};
}

describe("newestExperiment", () => {
	const list = [
		status("e_mine_ab12", "2026-11-10T00:00:00Z"),
		status("e_admit_zz_test_qa_4kha", "2026-11-12T00:00:00Z"),
		status("e_replay_r_abc123_9xyz", "2026-11-13T00:00:00Z"),
	];
	it("newestExperiment skips readiness checks", () => {
		expect(newestExperiment(list)?.exp_id).toBe("e_mine_ab12");
	});
	it("lists every experiment when asked", () => {
		expect(newestExperiment(list, { skipHidden: false })?.exp_id).toBe("e_replay_r_abc123_9xyz");
	});
	it("is null when only checks exist", () => {
		expect(newestExperiment(list.slice(1))).toBeNull();
	});
});