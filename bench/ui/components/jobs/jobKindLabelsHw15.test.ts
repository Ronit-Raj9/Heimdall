import { describe, expect, it } from "vitest";
import { jobKindLabelFor } from "./jobKindLabels";

describe("job kind labels for estimates (N275)", () => {
	it("labels a job whose params ask for an estimate as a cost estimate", () => {
		expect(jobKindLabelFor("policy.generate", { estimate: true })).toBe("Benchmark from policy document (Cost estimate)");
		expect(jobKindLabelFor("policy.generate", { yes: true })).toBe("Benchmark from policy document");
		expect(jobKindLabelFor("policy.generate", undefined)).toBe("Benchmark from policy document");
		expect(jobKindLabelFor("authoring.assist", { estimate: true }, "Draft with a model")).toBe("Draft with a model (Cost estimate)");
	});
});