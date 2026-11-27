import { describe, expect, it } from "vitest";
import { evidenceIsEmpty } from "./runEvidenceStatus";

describe("evidence block collapses when nothing was recorded (M4-59)", () => {
	it("is empty only with no contract, no hashes and no fault record", () => {
		expect(evidenceIsEmpty({})).toBe(true);
		expect(evidenceIsEmpty({ evidence_contract: { quality: "full" } } as never)).toBe(false);
		expect(evidenceIsEmpty({ adapter_fault: { entries: [], complete: true } } as never)).toBe(false);
	});
});
