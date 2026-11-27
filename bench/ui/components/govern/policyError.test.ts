import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { policyErrorText } from "./policyError";

describe("policyErrorText (Q-265)", () => {
	const t = i18n.getFixedT("en", "govern") as (k: string) => string;
	it("says the schedules file is not a rule set", () => {
		expect(policyErrorText("schedules.yaml", "field schedules not found in type policy.Policy", t)).toBe(
			"This file is not a rule set (it holds schedules). Ignore it or fix it in Edit.",
		);
		expect(policyErrorText("policies/schedules.yml", undefined, t)).toContain("not a rule set");
	});
	it("keeps other errors", () => {
		expect(policyErrorText("ship.yaml", "bad metric", t)).toBe("bad metric");
	});
});
