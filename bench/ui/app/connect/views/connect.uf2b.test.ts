import { describe, expect, it } from "vitest";
import { nameAfterDetect } from "./connectView";
import { firstLookEstimate, firstLookUsd } from "./firstLookLauncher";

// Fix wave lane 2b: Connect keeps a typed name; the first look says time and, when known, cost.
describe("connect helpers (fix wave 2b)", () => {
	it("keeps a name the person typed over a new suggestion", () => {
		expect(nameAfterDetect("my-agent", "", "support-bot")).toBe("support-bot");
		expect(nameAfterDetect("support-bot", "support-bot", "other-bot")).toBe("other-bot");
		expect(nameAfterDetect("mine", "support-bot", "other-bot")).toBe("mine");
	});

	it("gives the first look's time and cost", () => {
		expect(firstLookEstimate(0, 1)).toEqual({ kind: "unknown" });
		expect(firstLookEstimate(4, undefined)).toEqual({ kind: "time", minutes: 4 });
		expect(firstLookEstimate(4, 0.4)).toEqual({ kind: "cost", minutes: 4, usd: "0.40" });
		expect(firstLookEstimate(4, { low: 0.4, high: 1.1, known: true })).toEqual({ kind: "cost", minutes: 4, usd: "1.10" });
		expect(firstLookEstimate(4, { known: false, reason: "no price" })).toEqual({ kind: "time", minutes: 4 });
	});

	it("reads the server estimate_usd as optional", () => {
		expect(firstLookUsd(undefined)).toBeUndefined();
		expect(firstLookUsd(null)).toBeUndefined();
		expect(firstLookUsd({ low: 0.4, known: true })).toBe(0.4);
		expect(firstLookUsd(Number.NaN)).toBeUndefined();
	});
});
