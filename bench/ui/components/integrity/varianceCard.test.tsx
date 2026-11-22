import "@/lib/i18n";

import { verdictWith } from "@/components/verdict/testFixtures";
import type { Effect, VarianceCard as VarianceCardType, Verdict } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { VarianceCard } from "./varianceCard";

function render(verdict: Verdict): string {
	return renderToStaticMarkup(<VarianceCard verdict={verdict} />);
}

const aaEffect = {
	condition: "clean",
	metric: "verified_success",
	arm_a: "A",
	arm_b: "A2",
	n_tasks: 8,
	n_nonzero: 2,
	n_positive: 1,
	n_negative: 1,
	d_bar: 0.02,
	per_task: [],
	descriptive: false,
} as unknown as Effect;

function variance(overrides: Partial<VarianceCardType>): VarianceCardType {
	return { source: "assumed", sd: 0.3, assumed_sd: 0.3, mde: 34.6, detail: "", ...overrides };
}

describe("VarianceCard", () => {
	it("falls back to the verdict's SD on an analysis without a card", () => {
		const html = render(verdictWith({ variance: undefined, mde_assumed_sd: true, sd: 0.3 }));
		expect(html).toContain('data-source="assumed"');
		expect(html).toContain("predates the noise-floor card");
		expect(html).toContain("0.30");
	});
	it("says no A/A arm ran when the SD is assumed", () => {
		const html = render(verdictWith({ variance: variance({}) }));
		expect(html).toContain("No A/A setup ran");
		expect(html).toContain("assumed");
		expect(html).not.toContain('data-testid="variance-aa-tasks"');
	});
	it("shows the measured SD and the A/A comparison", () => {
		const html = render(
			verdictWith({
				variance: variance({
					source: "measured_aa",
					sd: 0.12,
					measured_sd: 0.12,
					aa_arm: "A2",
					aa_tasks: 8,
					aa_effect: aaEffect,
					aa_decision: "inconclusive",
					mde: 13.8,
					detail: "SD measured from 8 A/A tasks.",
				}),
			}),
		);
		expect(html).toContain('data-source="measured_aa"');
		expect(html).toContain("measured from the A/A setup");
		expect(html).toContain("0.12");
		expect(html).toContain("no difference");
		expect(html).toContain('data-testid="variance-band-aa"');
		expect(html).not.toContain('data-testid="variance-harness-noise"');
	});
	it("flags a directional A/A result as harness noise", () => {
		const html = render(
			verdictWith({
				variance: variance({
					source: "measured_aa",
					measured_sd: 0.2,
					aa_arm: "A2",
					aa_tasks: 8,
					aa_effect: aaEffect,
					aa_decision: "regressed",
				}),
			}),
		);
		expect(html).toContain('data-testid="variance-harness-noise"');
		expect(html).toContain("harness noise");
	});
	it("places the release effect against ±MDE", () => {
		const base = verdictWith({ variance: variance({ mde: 34.6 }) });
		const inside = render({ ...base, effect: { ...(base.effect as Effect), d_bar: 0.1 } });
		const outside = render({ ...base, effect: { ...(base.effect as Effect), d_bar: 0.5 } });
		expect(inside).toContain('data-inside="true"');
		expect(outside).toContain('data-inside="false"');
	});
});
describe("VarianceCard never claims an A/A measurement it does not have (N68)", () => {
	it("draws no source badge on an old analysis whose SD was not assumed", () => {
		const html = render(verdictWith({ variance: undefined, mde_assumed_sd: false, sd: 0, mde: 0 }));
		expect(html).not.toContain("measured from the A/A setup");
		expect(html).not.toContain('data-testid="variance-source"');
		expect(html).not.toContain('data-source="measured_aa"');
	});
});