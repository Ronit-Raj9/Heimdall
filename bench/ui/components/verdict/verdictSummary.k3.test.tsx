import "@/lib/i18n";
import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { ScoreSummary } from "./scoreSummary";
import { stoppedByHand } from "./verdictSummary";

const scoreVerdict = (decision: string) =>
	({ decision, invalid_reasons: ["coverage"], score: { arm: "A", metric: "pass_rate", mean: 1, runs: 3, observed: 2, tasks: 3 } }) as never;

describe("verdict labels (K3)", () => {
	it("a comparison stopped by hand reads Stopped, not broken", () => {
		expect(stoppedByHand({ decision: "invalid" } as never, false, true)).toBe(true);
		expect(stoppedByHand({ decision: "invalid" } as never, false, false)).toBe(false);
		expect(stoppedByHand({ decision: "invalid" } as never, true, true)).toBe(false);
		expect(stoppedByHand({ decision: "improved" } as never, false, true)).toBe(false);
	});
	it("a score still running with solved runs does not say runs broke (R1)", () => {
		const html = render(<ScoreSummary verdict={scoreVerdict("invalid")} naive={{ A: { num: 2, den: 2 } } as never} partial />);
		expect(html).not.toContain("outside the agent");
	});
	it("a stopped score says it was stopped", () => {
		const html = render(<ScoreSummary verdict={scoreVerdict("invalid")} naive={{} as never} stopped />);
		expect(html).toContain("You stopped this");
		expect(html).not.toContain("outside the agent");
	});
});