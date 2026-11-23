import { describe, expect, it } from "vitest";
import { plainJobError } from "./jobRunButton";

describe("job start errors in plain words (N238)", () => {
	it("drops the fixed create-job prefix", () => {
		expect(
			plainJobError(
				"create job: invalid job parameters: Your organization has no working anthropic model key. Add one on the Models page.",
			),
		).toBe("Your organization has no working anthropic model key. Add one on the Models page.");
		expect(plainJobError("the job queue is full")).toBe("the job queue is full");
	});
});