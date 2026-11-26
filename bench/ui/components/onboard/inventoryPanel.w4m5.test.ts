import i18n from "@/lib/i18n";

import type { OnboardInventory } from "@/lib/types/onboard";
import { describe, expect, it } from "vitest";
import { estimateLine } from "./inventoryPanel";

const tx = (k: string, o?: Record<string, unknown>) => i18n.t(k as never, { ns: "onboard", ...o } as never) as unknown as string;
const inv = { estimate: { tasks: 27, readiness_runs: 81, judge_calls: 0, assist_usd: 0 } } as OnboardInventory;

describe("estimateLine (p10-1, p10-9c)", () => {
	it("follows the chosen grader", () => {
		const judge = estimateLine(tx, inv, "judge");
		expect(judge).toContain("A rubric judge grades the readiness runs");
		expect(judge).not.toContain("cost nothing");
		expect(estimateLine(tx, inv, "answer_match")).toContain("so they cost nothing");
	});
	it("is empty when no task was found", () => {
		expect(estimateLine(tx, { ...inv, estimate: { ...inv.estimate, tasks: 0 } }, "answer_match")).toBe("");
	});
});