import "@/lib/i18n";
import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { JudgeCriteria } from "./judgeCriteria";

const judge = {
	model: "judge-model",
	prompt_version: "custom-v1",
	prompt_sha256: "a".repeat(64),
	votes: ["incorrect"],
	usd: { value: 0.001, evidence: "gateway" },
	score: 0.75,
	pass_at: 1,
	criteria: [
		{ id: "c1", text: "Apologises for the error", weight: 2, met: true, reason: "says sorry" },
		{ id: "c3", text: "States the refund timeline", weight: 1, met: false, reason: "no timeline given" },
	],
} as never;

describe("JudgeCriteria", () => {
	it("lists each criterion with its weight, verdict and reason", () => {
		const html = render(<JudgeCriteria judge={judge} />);
		expect(html).toContain('data-testid="judge-criteria-c1"');
		expect(html).toContain('data-met="false"');
		expect(html).toContain("Apologises for the error");
		expect(html).toContain("weight 2");
		expect(html).toContain("no timeline given");
		expect(html).toContain("75% of the weight met, 100% needed");
	});

	it("renders nothing for a judge without criteria", () => {
		expect(render(<JudgeCriteria judge={{ ...(judge as object), criteria: [] } as never} />)).toBe("");
		expect(render(<JudgeCriteria judge={undefined} />)).toBe("");
	});
});
