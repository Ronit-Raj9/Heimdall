import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReviewQueueTable } from "./reviewQueueTable";
import { queueFixture } from "./testFixtures";

describe("ReviewQueueTable", () => {
	const html = renderToStaticMarkup(<ReviewQueueTable items={queueFixture.items} selectedRunId="r_pww2l7yame" onSelect={() => {}} />);

	it("lists every queued run with task, arm, condition and reasons", () => {
		for (const item of queueFixture.items) {
			expect(html).toContain(`data-testid="review-queue-row-${item.run_id}"`);
			for (const reason of item.reasons) expect(html).toContain(`data-testid="review-queue-reason-${item.run_id}-${reason}"`);
		}
		expect(html).toContain(">t02<");
		expect(html).toContain(">stale-notes<");
		expect(html).toContain("False completion");
		expect(html).toContain("Flagged");
	});

	it("shows the sealed outcome beside the outcome in effect and the decision", () => {
		expect(html).toMatch(/data-testid="review-queue-sealed-r_fmw1z7ouim"[^>]*data-outcome="fail"/);
		expect(html).toMatch(/data-testid="review-queue-current-r_fmw1z7ouim"[^>]*data-outcome="pass"/);
		expect(html).toMatch(/data-testid="review-queue-decision-r_fmw1z7ouim"[^>]*>.*Pass/);
		expect(html).toMatch(/data-testid="review-queue-decision-r_pww2l7yame"[^>]*>.*none/);
	});

	it("marks the selected row", () => {
		expect(html).toMatch(/aria-selected="true"[^>]*data-testid="review-queue-row-r_pww2l7yame"/);
		expect(html).toMatch(/aria-selected="false"[^>]*data-testid="review-queue-row-r_fmw1z7ouim"/);
	});
});