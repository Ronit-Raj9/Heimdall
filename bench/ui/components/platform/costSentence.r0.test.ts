import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { costSentence } from "./confirmDialog";

const i18nT = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

describe("costSentence", () => {
	const t = i18nT;
	it("names a known cap", () => {
		expect(costSentence(t, 5)).toBe("This can spend up to $5.00 on your model keys.");
	});
	it("states the estimate and what limits the spend when there is no cap", () => {
		expect(costSentence(t, null, { estimateUsd: 7.17, estimateRuns: 130 })).toBe("About $7.17 for 130 runs. It stops when those runs are done.");
		expect(costSentence(t, null, { estimateUsd: 2, limitText: "It stops at 10 minutes." })).toBe("About $2.00. It stops at 10 minutes.");
	});
	it("warns only when nothing limits the spend", () => {
		expect(costSentence(t, null)).toMatch(/^Warning: .*No cap or estimate/);
		expect(costSentence(t, undefined)).toBeNull();
	});
});
