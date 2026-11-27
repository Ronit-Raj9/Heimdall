import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { CostExplanation } from "@/lib/types/bench";
import { CostExplanationView } from "./costWaterfall";
import { COST_FIXTURE } from "./migration.fixtures";

const render = (e: CostExplanation) => renderToStaticMarkup(<CostExplanationView explanation={e} />);

describe("CostExplanationView", () => {
	it("shows both arms, every part and the total of the recorded pilot", () => {
		const html = render(COST_FIXTURE);
		expect(html).toContain("A (gpt-5.4-mini)");
		expect(html).toContain("B (gpt-5.4-mini)");
		// Parts that are exactly $0 in the fixture (price, turns, wasted attempts) are left out (Q-157).
		for (const id of ["price", "turns", "wasted_attempts"]) expect(html).not.toContain(`data-testid="cost-waterfall-run-part-${id}"`);
		for (const id of ["input_tokens", "output_tokens", "cache_read_share"]) {
			expect(html).toContain(`data-testid="cost-waterfall-run-part-${id}"`);
		}
		expect(html).not.toContain('data-testid="cost-waterfall-success-part-success_rate"');
		expect(html).toMatch(/data-testid="cost-waterfall-run-total"[\s\S]*?-\$0\.02/);
		expect(html).toContain("No cache break. Read share never dropped 10 points or more.");
	});

	it("draws an unknown part as a hatched range, never as zero", () => {
		const e: CostExplanation = {
			...COST_FIXTURE,
			per_run: {
				...COST_FIXTURE.per_run,
				parts: [
					...COST_FIXTURE.per_run.parts,
					{
						id: "unpriced",
						label: "Cost the rows do not know",
						usd: { state: "unknown", v: null, bounds: [0, 0.05], reason: "1 unpriced row" },
						detail: "",
					},
				],
				total: { state: "unknown", v: null, bounds: [-0.021, 0.029] },
			},
		};
		const html = render(e);
		expect(html).toMatch(/data-testid="cost-waterfall-run-part-unpriced"[\s\S]*?data-known="false"/);
		expect(html).toContain("$0 to $0.05");
		expect(html).toMatch(/data-testid="cost-waterfall-run-total"[\s\S]*?-\$0\.02 to \$0\.03/);
	});

	it("names a cache break and its likely cause, and says why a waterfall does not apply", () => {
		const e: CostExplanation = {
			...COST_FIXTURE,
			cache: [
				{
					kind: "between_arms",
					arm: "B",
					baseline: "A",
					share_before: 0.896,
					share_after: 0.737,
					cause: "system prompt changed: sha256 aaa vs bbb in the gateway's raw rows",
					evidence: ["config diff: prompt_notes changed (notes.user_path)"],
				},
			],
			per_success: { ...COST_FIXTURE.per_success, reason: "cost per verified success is undefined: arm A has 0 of 3 verified successes" },
		};
		const html = render(e);
		expect(html).toContain("Cache read share of setup B dropped to 73.7% from 89.6% (A).");
		expect(html).toContain("system prompt changed");
		expect(html).toContain("Not applicable: cost per verified success is undefined");
	});
});