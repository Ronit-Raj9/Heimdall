import { describe, expect, it } from "vitest";
import { jobPollMs } from "./liveJob";

describe("jobPollMs (M1-62)", () => {
	it("polls a running or unknown job and stops at the end", () => {
		expect(jobPollMs("running", 2000)).toBe(2000);
		expect(jobPollMs(undefined, 2000)).toBe(2000);
		expect(jobPollMs("succeeded", 2000)).toBe(0);
		expect(jobPollMs("failed", 2000)).toBe(0);
	});
});
