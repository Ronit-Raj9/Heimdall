import "@/lib/i18n";

import type { Score } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ScoreSummary } from "./scoreSummary";
import { verdictWith } from "./testFixtures";
import { formatScoreValue, isScoreVerdict, scoreBounds } from "./verdict.utils";

const score: Score = {
	arm: "A",
	label: "codex on gpt-x",
	condition: "clean",
	metric: "verified_success",
	mean: 0.625,
	interval: [0.4, 0.8],
	conf: 0.95,
	method: "task-clustered percentile bootstrap over tasks (95%)",
	low: 0.55,
	high: 0.7,
	tasks: 8,
	runs: 16,
	observed: 15,
};

const render = (decision: "score" | "invalid", s: Score = score) => {
	const verdict = verdictWith({ decision, action: decision === "score" ? "no_decision" : "fix_harness", effect: undefined, score: s });
	if (!isScoreVerdict(verdict)) throw new Error("not a score verdict");
	return renderToStaticMarkup(<ScoreSummary verdict={verdict} naive={{ A: { num: 10, den: 16, unknown: 1 } }} />);
};

describe("ScoreSummary", () => {
	it("shows the score and its range, never a comparison", () => {
		const html = render("score");
		expect(html).toContain('data-decision="score"');
		expect(html).toContain("62.5%");
		expect(html).toContain("95% range: 40% to 80%");
		expect(html).toContain("15 of 16 runs graded, over 8 tasks");
		expect(html).toContain("codex on gpt-x");
		expect(html).not.toMatch(/ vs |is better|is worse|No clear winner/);
	});
	it("says when the range is unknown and when the test broke", () => {
		expect(render("score", { ...score, interval: undefined })).toContain("Range not known");
		expect(render("invalid")).toContain("Some runs broke for reasons outside the agent");
	});
	it("says why a cost is missing and blames the price only for an unpriced model", () => {
		const verdict = verdictWith({ decision: "score", action: "no_decision", effect: undefined, score });
		if (!isScoreVerdict(verdict)) throw new Error("not a score verdict");
		const withCost = (reason: string) =>
			renderToStaticMarkup(
				<ScoreSummary
					verdict={verdict}
					naive={{ A: { num: 10, den: 16, unknown: 1 } }}
					armMetrics={{ A: { cost_usd: { state: "unknown", v: null, reason } } }}
				/>,
			);
		const lost = withCost("telemetry_incomplete");
		expect(lost).toContain("Not known: telemetry_incomplete");
		expect(lost).not.toContain("no price set");
		expect(withCost("unpriced_model")).toContain("Not known: no price set");
	});
});

describe("score helpers", () => {
	it("formats rates as percentages and other metrics in their unit", () => {
		expect(formatScoreValue("verified_success", 0.5, "en")).toBe("50%");
		expect(formatScoreValue("cost_usd", 0.0312, "en")).toBe("0.0312 USD");
		expect(formatScoreValue("steps", 12, "en")).toBe("12");
	});
	it("gives bounds only when some runs were not graded", () => {
		expect(scoreBounds(score)).toEqual([0.55, 0.7]);
		expect(scoreBounds({ ...score, observed: 16 })).toBeNull();
		expect(isScoreVerdict(verdictWith({}))).toBe(false);
	});
});