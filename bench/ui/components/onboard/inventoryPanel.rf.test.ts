import i18n from "@/lib/i18n";

import type { OnboardInventory } from "@/lib/types/onboard";
import { describe, expect, it } from "vitest";
import { estimateLine } from "./inventoryPanel";

const tx = (k: string, o?: Record<string, unknown>) => i18n.t(k as never, { ns: "onboard", ...o } as never) as unknown as string;

describe("readiness estimate (Q-57)", () => {
	it("says the readiness runs are free and gives no time, since none is measured", () => {
		const inv = { estimate: { tasks: 27, readiness_runs: 81, judge_calls: 0, assist_usd: 0 } } as OnboardInventory;
		const line = estimateLine(tx, inv, "answer_match");
		expect(line).toContain("81 readiness runs");
		expect(line).toContain("Both are free.");
		expect(line).not.toMatch(/\bmin\b/);
	});
});
