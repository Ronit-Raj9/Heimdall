import { describe, expect, it } from "vitest";
import { CANARY_DRIFTED_FIXTURE, CANARY_LIST_FIXTURE } from "./canary.fixtures";
import { formatP, formatShift, latestComparedLook, latestEquivalence, sortCanaries, stateVariant, targetLabel } from "./canary.utils";

describe("canary utils", () => {
	it("puts a drifted canary first and maps states to badge variants", () => {
		expect(sortCanaries(CANARY_LIST_FIXTURE.canaries).map((c) => c.name)).toEqual(["nightly", "smoke-toy"]);
		expect(stateVariant("drifted")).toBe("destructive");
		expect(stateVariant("steady")).toBe("success");
		expect(stateVariant("watching")).toBe("warning");
		expect(stateVariant("something-new")).toBe("secondary");
	});

	it("finds the newest compared look and the newest equivalence report", () => {
		const look = latestComparedLook(CANARY_DRIFTED_FIXTURE);
		expect(look?.kind).toBe("confirmation");
		expect(look?.comparison?.flagged).toContain("A/clean/verified_success");
		expect(latestEquivalence(CANARY_DRIFTED_FIXTURE)?.equivalence?.[0].verdict).toBe("differs");
	});

	it("formats p-values, shifts and targets", () => {
		expect(formatP(0.0078125)).toBe("0.0078");
		expect(formatP(null)).toBeNull();
		expect(formatShift(0.6, "en-US")).toBe("+60%");
		expect(targetLabel({ model: "m" })).toBe("m");
		expect(targetLabel({ provider: "p", model: "m" })).toBe("p/m");
	});
});