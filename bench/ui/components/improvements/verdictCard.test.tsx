import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { HEADLINES, IMPROVEMENT_FIXTURE, stateFixture } from "./improve.fixtures";
import { VERDICT_STATES } from "./improve.utils";
import { VerdictCard } from "./verdictCard";

describe("VerdictCard", () => {
	it.each(VERDICT_STATES)("renders the %s headline from the fixture", (state) => {
		const html = render(<VerdictCard data={stateFixture(state)} onContinue={() => undefined} />);
		expect(html).toContain(HEADLINES[state]);
		expect(html).toContain('data-testid="verdict-headline"');
		expect(html).toContain("One run of the loop.");
	});

	it("shows Continue only when the run can continue", () => {
		const offer = stateFixture("likely_better");
		expect(render(<VerdictCard data={offer} onContinue={() => undefined} />)).toContain('data-testid="verdict-continue"');
		expect(render(<VerdictCard data={{ ...offer, can_continue: false }} onContinue={() => undefined} />)).not.toContain(
			'data-testid="verdict-continue"',
		);
		expect(render(<VerdictCard data={stateFixture("supported")} onContinue={() => undefined} />)).not.toContain(
			'data-testid="verdict-continue"',
		);
	});

	it("shows an MDE of 0 with fewer than 2 tasks as not enough tasks to estimate", () => {
		const d = stateFixture("insufficient_sample");
		const html = render(<VerdictCard data={{ ...d, verdict_detail: { ...d.verdict_detail!, mde: 0, n: 1 } }} />);
		expect(html).toContain("not enough tasks to estimate");
		expect(html).not.toMatch(/Smallest gain it can confirm<\/dt><dd[^>]*>0</);
	});

	it("reads an older run's headline and notes", () => {
		const html = render(<VerdictCard data={IMPROVEMENT_FIXTURE} />);
		expect(html).toContain("improvement supported: +20.0 points");
	});
});