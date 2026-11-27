import { decisionTone } from "@/components/verdict/verdict.utils";
import { describe, expect, it } from "vitest";
import { isUnseen, lastScanAt } from "./validityView";

describe("validity page helpers", () => {
	it("finds the newest scan time and treats no stale rows as never scanned", () => {
		expect(lastScanAt([])).toBeNull();
		expect(
			lastScanAt([
				{ exp_id: "a", gates: {}, checked: "2026-11-20T10:00:00Z" },
				{ exp_id: "b", gates: {}, checked: "2026-11-23T10:00:00Z" },
			]),
		).toBe("2026-11-23T10:00:00Z");
	});

	it("tones decision words and marks unseen flips", () => {
		expect(decisionTone("improved")).toBe("good");
		expect(decisionTone("regressed")).toBe("bad");
		expect(decisionTone("something-new")).toBe("neutral");
		expect(decisionTone(undefined)).toBe("neutral");
		const e = {
			id: "1",
			exp_id: "e",
			regrade_id: "r",
			before: { source: "s", decision: "improved" },
			after: { source: "s", decision: "regressed" },
			flipped: true,
			at: "",
		};
		expect(isUnseen(e, "u")).toBe(true);
		expect(isUnseen({ ...e, seen_by: ["u"] }, "u")).toBe(false);
	});
});