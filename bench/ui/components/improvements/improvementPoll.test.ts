import { describe, expect, it } from "vitest";
import { improvementPollMs, isLiveImprovement, listPollMs } from "./improvementPoll";

describe("improvement polling (M1-62)", () => {
	it("polls every 5 s while the run can change, and stops once it is final", () => {
		expect(improvementPollMs(undefined)).toBe(5000);
		expect(improvementPollMs("running")).toBe(5000);
		expect(improvementPollMs("awaiting_approval")).toBe(5000);
		expect(improvementPollMs("done")).toBe(0);
		expect(improvementPollMs("failed")).toBe(0);
		expect(isLiveImprovement("stopped")).toBe(false);
		expect(isLiveImprovement("planned")).toBe(true);
	});
});

describe("list polling (M1-62)", () => {
	it("polls fast only while a row is live", () => {
		expect(listPollMs(true)).toBe(5000);
		expect(listPollMs(false)).toBe(30_000);
	});
});
