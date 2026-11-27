import { describe, expect, it } from "vitest";
import { jobCountFrom } from "./jobCountApi";

describe("jobCountFrom (X-108)", () => {
	it("reads the count and the more flag", () => {
		expect(jobCountFrom({ count: 3, more: true })).toEqual({ count: 3, more: true });
		expect(jobCountFrom({ count: 0 })).toEqual({ count: 0, more: false });
	});
	it("gives null when an older server sends the job list instead", () => {
		expect(jobCountFrom({ jobs: [] })).toEqual({ count: null, more: false });
		expect(jobCountFrom(undefined)).toEqual({ count: null, more: false });
	});
});
