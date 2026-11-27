import { describe, expect, it } from "vitest";
import { agentRunsParams, failReasonOf } from "./agentRunsTab";

describe("agent runs tab: why it failed (M2-42)", () => {
	it("prefers the server reason, then the error, then the outcome", () => {
		expect(failReasonOf({ outcome: "fail", fail_reason: "Wrong total", error: "boom" })).toEqual({ text: "Wrong total" });
		expect(failReasonOf({ outcome: "crash", error: "exit 1" })).toEqual({ text: "exit 1" });
		expect(failReasonOf({ outcome: "fail" })).toEqual({ key: "runs.reason.fail" });
		expect(failReasonOf({ outcome: "integrity_violation" })).toEqual({ key: "runs.reason.integrity_violation" });
		expect(failReasonOf({ outcome: "unknown" })).toEqual({ key: "runs.reason.other" });
	});

	it("is empty for a pass", () => {
		expect(failReasonOf({ outcome: "pass", error: "x" })).toEqual({});
	});
});

describe("agent runs tab: filters (M2-45, M2-47)", () => {
	it("sends the benchmark and result only when picked", () => {
		expect(agentRunsParams("a", 0, false)).toEqual({ agent: "a", limit: 50, offset: 0 });
		expect(agentRunsParams("a", 2, true, { suite: "s1", outcome: "fail" })).toEqual({
			agent: "a",
			limit: 50,
			offset: 100,
			suite: "s1",
			outcome: "fail",
			failed: true,
		});
	});
});
