import "@/lib/i18n";

import type { AgentCoverage } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { CoverageBadge } from "./coverageBadge";

const coverage: AgentCoverage = {
	steps: 24,
	attributed: 22,
	unknown: 2,
	by_source: { gateway_key: 20, self_report: 2 },
	by_strength: { proven: 20, reported: 2 },
};

const render = (c: AgentCoverage) => renderToStaticMarkup(<CoverageBadge coverage={c} testId="cov" />);

describe("CoverageBadge", () => {
	const tones = [
		{ attributed: 24, want: "good" },
		{ attributed: 23, want: "good" },
		{ attributed: 22, want: "warn" },
		{ attributed: 10, want: "bad" },
	];
	for (const c of tones) {
		it(`tones ${c.attributed} of 24 as ${c.want}`, () => {
			const html = render({ ...coverage, attributed: c.attributed });
			expect(html).toContain(`data-testid="cov" data-tone="${c.want}"`);
		});
	}

	it("shows the percent, counts, unknown rows and the breakdown by strength and source", () => {
		const html = render(coverage);
		expect(html).toContain("92% of steps attributed");
		expect(html).toContain("22 of 24 steps");
		expect(html).toContain('data-testid="cov-unknown"');
		expect(html).toContain("2 unknown");
		expect(html).toContain('data-testid="cov-strength-proven"');
		expect(html).toContain('data-testid="cov-strength-reported"');
		expect(html).toContain("By strength: proven 20, reported 2. By source: gateway key 20, self-report 2. 2 unknown");
		expect(html).toContain('data-testid="cov-meter"');
	});

	it("is neutral with no steps and hides the meter", () => {
		const html = render({ steps: 0, attributed: 0, unknown: 0 });
		expect(html).toContain('data-tone="neutral"');
		expect(html).toContain("no steps to attribute");
		expect(html).not.toContain('data-testid="cov-meter"');
		expect(html).not.toContain('data-testid="cov-unknown"');
	});
});