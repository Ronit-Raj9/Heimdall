import i18n from "@/lib/i18n";

import type { OnboardInventory } from "@/lib/types/onboard";
import { describe, expect, it } from "vitest";
import { estimateLine, readinessMinutes } from "./inventoryPanel";

const tx = (k: string, o?: Record<string, unknown>) => i18n.t(k as never, { ns: "onboard", ...o } as never) as unknown as string;

describe("readiness time (Q-57)", () => {
	it("gives about ten runs a minute, at least one minute", () => {
		expect(readinessMinutes(81)).toBe(9);
		expect(readinessMinutes(3)).toBe(1);
	});
	it("the estimate names the minutes", () => {
		const inv = { estimate: { tasks: 27, readiness_runs: 81, judge_calls: 0, assist_usd: 0 } } as OnboardInventory;
		expect(estimateLine(tx, inv, "answer_match")).toContain("cost nothing and take about 9 min");
	});
});
