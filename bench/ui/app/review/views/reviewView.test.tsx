import "@/lib/i18n";

import { queueFixture } from "@/components/review/testFixtures";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { QueueBody } from "./reviewView";

const noop = () => {};

describe("QueueBody", () => {
	it("lists the queue, the reason filter and asks to pick a run", () => {
		const html = renderToStaticMarkup(<QueueBody data={queueFixture} reason={null} onReason={noop} runId={null} onRun={noop} />);
		expect(html).toContain('data-testid="review-queue-table"');
		expect(html).toContain('data-testid="review-queue-row-r_fmw1z7ouim"');
		expect(html).toContain('data-testid="review-queue-row-r_pww2l7yame"');
		expect(html).toContain('data-testid="review-reason-filter"');
		expect(html).toContain('data-testid="review-select-run"');
		expect(html).not.toContain('data-testid="mode-badge-synthetic"');
	});

	it("filters by reason on the client", () => {
		const html = renderToStaticMarkup(<QueueBody data={queueFixture} reason="flagged" onReason={noop} runId={null} onRun={noop} />);
		expect(html).toContain('data-testid="review-queue-row-r_pww2l7yame"');
		expect(html).not.toContain('data-testid="review-queue-row-r_fmw1z7ouim"');
	});

	it("says when no run has the reason", () => {
		const html = renderToStaticMarkup(<QueueBody data={queueFixture} reason="veto" onReason={noop} runId={null} onRun={noop} />);
		expect(html).toContain('data-testid="review-queue-empty-filtered"');
	});

	it("lists reviews that were not applied", () => {
		const data = { ...queueFixture, problems: ["review rv_x: parent seal differs from the run seal"] };
		const html = renderToStaticMarkup(<QueueBody data={data} reason={null} onReason={noop} runId={null} onRun={noop} />);
		expect(html).toContain('data-testid="review-queue-problems"');
		expect(html).toContain("parent seal differs");
	});

	it("names a run a safety check threw out in the filter and the queue (fix wave 3, N257)", () => {
		const item = {
			...queueFixture.items[0],
			run_id: "r_thrown",
			reasons: ["invalidated"],
			thrown_out_by: ["model check: served by another model"],
		};
		const data = { ...queueFixture, items: [...queueFixture.items, item], reasons: [...(queueFixture.reasons ?? []), "invalidated"] };
		const html = renderToStaticMarkup(<QueueBody data={data} reason={null} onReason={noop} runId={null} onRun={noop} />);
		expect(html).toMatch(/data-testid="review-reason-option-invalidated"[^>]*>[^<]*Thrown out by a safety check/);
		expect(html).toContain("Thrown out by a safety check: model check: served by another model");
		expect(html).not.toContain(">invalidated<");
	});
});