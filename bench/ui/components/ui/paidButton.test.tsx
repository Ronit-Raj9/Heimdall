import { describe, expect, it } from "vitest";
import { needsPaidConfirm, paidEstimate, paidLabel } from "@/components/ui/paidButton";

const t = (k: string, o?: Record<string, unknown>) => `${k}|${JSON.stringify(o ?? {})}`;

describe("needsPaidConfirm (M1-51)", () => {
	it("auto confirms over $1 or when the cap is unknown", () => {
		expect(needsPaidConfirm("auto", 5)).toBe(true);
		expect(needsPaidConfirm("auto", null)).toBe(true);
		expect(needsPaidConfirm("auto", 1)).toBe(false);
		expect(needsPaidConfirm("auto", 0.2)).toBe(false);
	});
	it("always and never are fixed", () => {
		expect(needsPaidConfirm("always", 0.01)).toBe(true);
		expect(needsPaidConfirm("never", 100)).toBe(false);
	});
});

describe("paidLabel and paidEstimate", () => {
	it("names the cap with the money format", () => {
		expect(paidLabel(t, "Run test", 5)).toBe('label|{"verb":"Run test","cap":"$5.00"}');
		expect(paidLabel(t, "Run test", null)).toBe('labelUnknown|{"verb":"Run test"}');
	});
	it("shows what is known of the estimate", () => {
		expect(paidEstimate(t, 24, 1.2)).toBe('estimate|{"runs":24,"usd":"$1.20"}');
		expect(paidEstimate(t, 24, null)).toBe('estimateRuns|{"runs":24}');
		expect(paidEstimate(t, undefined, 0.5)).toBe('estimateUsd|{"usd":"$0.50"}');
		expect(paidEstimate(t, undefined, undefined)).toBeNull();
	});
});
