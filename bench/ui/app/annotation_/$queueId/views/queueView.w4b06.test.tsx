import type { ExperimentStatus } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { finishedRunsOf } from "./queueView";

function done(exp_id: string): ExperimentStatus {
	return {
		exp_id,
		name: exp_id,
		state: "done",
		suite: "support-bench@1.0.0",
		manifest_hash: "h",
		frozen_at: "2026-11-10T00:00:00Z",
		slots: { total: 1, done: 1, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
	};
}

describe("finishedRunsOf", () => {
	it("finishedRunsOf drops admit runs", () => {
		const got = finishedRunsOf([done("e_mine_ab12"), done("e_admit_zz_test_qa_4kha"), done("e_check_zz_test_qa_1abc")], undefined);
		expect(got.map((e) => e.exp_id)).toEqual(["e_mine_ab12"]);
	});
});