import { describe, expect, it } from "vitest";
import { canRetryFree } from "./jobRetryCost";

describe("canRetryFree (R3-4)", () => {
	it("is free only for the debug replay of a run", () => {
		expect(canRetryFree({ kind: "experiment.run", params: { exp_id: "e_replay_r_fglg15huoc_blud" } })).toBe(true);
		expect(canRetryFree({ kind: "experiment.run", params: { exp_id: "e_fork_r_fglg15huoc_at_call_2_ab12" } })).toBe(false);
		expect(canRetryFree({ kind: "experiment.run", params: { exp_id: "e_support_run_ab12" } })).toBe(false);
		expect(canRetryFree({ kind: "replay.run", params: {} })).toBe(false);
		expect(canRetryFree({ kind: "suite.calibrate", params: { exp_id: "e_replay_r_x" } })).toBe(false);
	});
});
