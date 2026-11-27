import { describe, expect, it } from "vitest";
import { canRetryJob, firstErrorLine, jobBudget, jobTargetLabel, parseJobStatus } from "./jobsView.utils";

describe("jobs list helpers (fix wave lane 2a)", () => {
	it("offers Retry only on failed or cancelled jobs (N8-1)", () => {
		expect(canRetryJob({ status: "failed" })).toBe(true);
		expect(canRetryJob({ status: "cancelled" })).toBe(true);
		expect(canRetryJob({ status: "succeeded" })).toBe(false);
		expect(canRetryJob({ status: "running" })).toBe(false);
	});

	it("reads the spend cap for the Retry question (M2-1)", () => {
		expect(jobBudget({ params: { budget_usd: 2.5 } })).toBe("$2.50");
		expect(jobBudget({ params: { budget_usd: 0 } })).toBeNull();
		expect(jobBudget({ params: { budget_usd: "5" } })).toBeNull();
		expect(jobBudget({ params: null })).toBeNull();
	});

	it("strips internal prefixes from targets and drops the search index (M8-58)", () => {
		expect(jobTargetLabel("exp:e_eval_1")).toBe("e_eval_1");
		expect(jobTargetLabel("onboard:zz-test-p10-csv")).toBe("zz-test-p10-csv");
		expect(jobTargetLabel("suite:mini@1")).toBe("mini@1");
		expect(jobTargetLabel("index")).toBe("");
		expect(jobTargetLabel(undefined)).toBe("");
		expect(jobTargetLabel("codex")).toBe("codex");
	});

	it("keeps the first line of an error and reads URL filters (M2-3, M2-5)", () => {
		expect(firstErrorLine("gateway refused\nstack")).toBe("gateway refused");
		expect(firstErrorLine(undefined)).toBe("");
		expect(parseJobStatus("failed", ["failed", "running"] as const)).toBe("failed");
		expect(parseJobStatus("bogus", ["failed", "running"] as const)).toBe("");
	});
});
