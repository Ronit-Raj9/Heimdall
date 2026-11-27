import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { EstimateCard, estimateBlock } from "./estimateCard";
import { ESTIMATE_FIXTURE } from "./improve.fixtures";

describe("EstimateCard", () => {
	it("shows the splits, the smallest gain, the cost and the improver line, warnings in amber", () => {
		const html = render(<EstimateCard estimate={ESTIMATE_FIXTURE} model="gpt-5-mini" />);
		expect(html).toContain("60 tasks: 24 practice, 18 check, 18 hidden.");
		expect(html).toContain("Smallest confirmable gain: about 24 points.");
		expect(html).toContain("Estimated cost: $15.80 to $33.90, about 80 to 160 minutes. Hard stop at $45.00.");
		expect(html).toContain("Improver: Heimdall on gpt-5-mini.");
		expect(html).toContain('data-testid="estimate-warning"');
		expect(estimateBlock(ESTIMATE_FIXTURE)).toBeUndefined();
	});

	it("shows refusals in red and gives the first as the reason Create is disabled", () => {
		const refused = {
			...ESTIMATE_FIXTURE,
			ok: false,
			refusals: [
				{
					code: "too_few_held_out",
					message:
						"This benchmark gives 3 held-out tasks. With so few, no result could ever be shown to be better. Use a benchmark with at least 40 tasks, or add tasks.",
				},
				{ code: "too_few_practice", message: "The improver needs at least 4 practice tasks to learn from; this benchmark gives 2." },
			],
		};
		const html = render(<EstimateCard estimate={refused} model="m" />);
		expect(html).toContain('data-testid="estimate-refusal"');
		expect(estimateBlock(refused)).toBe(refused.refusals[0].message);
	});

	it("says the smallest gain cannot be estimated with fewer than 2 held-out tasks, never 0", () => {
		const tiny = {
			...ESTIMATE_FIXTURE,
			splits: { ...ESTIMATE_FIXTURE.splits, held_out: 1 },
			detectable: { gate_points: 0, held_out_points: 0 },
		};
		const html = render(<EstimateCard estimate={tiny} model="m" />);
		expect(html).toContain("Smallest confirmable gain: too few tasks.");
		expect(html).not.toContain("about 0 points");
	});

	it("says when the estimate is loading or not available", () => {
		expect(render(<EstimateCard loading model="m" />)).toContain("Estimating...");
		expect(render(<EstimateCard error="This server cannot estimate." model="m" />)).toContain(
			"The estimate is not available: This server cannot estimate.",
		);
	});
});