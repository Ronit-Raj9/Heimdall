import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { comparisonSummaryMarkdown } from "./comparisonSummary";

// Round 2 lane RC (Q-70): a readable comparison report from what the page has.
describe("comparisonSummaryMarkdown", () => {
	it("names the comparison, each setup's solved count and cost, and the link", () => {
		const md = comparisonSummaryMarkdown({
			title: "codex on two models",
			expId: "e1",
			suite: "coding-core@1.0.0",
			verdict: { decision: "inconclusive", action: "no_decision", guards: [] } as never,
			arms: ["A", "B"],
			naive: { A: { num: 7, den: 8, unknown: 0 }, B: { num: 7, den: 8, unknown: 0 } },
			names: { A: "codex on gpt-5", B: "codex on gpt-6-luna" },
			costs: { A: "$0.10", B: "$0.0063" },
			url: "https://example.test/experiments/e1",
			date: new Date("2026-11-27T00:00:00Z"),
		});
		expect(md).toContain("# codex on two models");
		expect(md).toContain("| A (codex on gpt-5) | 7 / 8 | $0.10 |");
		expect(md).toContain("Benchmark: coding-core@1.0.0");
		expect(md).toContain("Date: 2026-11-27");
		expect(md).toContain("Full result: https://example.test/experiments/e1");
	});
});