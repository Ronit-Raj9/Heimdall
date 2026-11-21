import "@/lib/i18n";

import type { Review, RunReviews } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReviewTimeline } from "./reviewTimeline";
import { flaggedRunReviews, passedRunReviews } from "./testFixtures";

const later: Review = {
	...passedRunReviews.reviews[0],
	review_id: "rv_synth0003",
	decision: "invalid",
	reason: "On a second look the verifier image was broken for this run.",
	reviewer: "sam",
	previous_outcome: "pass",
	supersedes: "rv_synth0002",
	prev_hash: passedRunReviews.reviews[0].hash,
	hash: "ff".repeat(32),
};

describe("ReviewTimeline", () => {
	it("shows the sealed and effective outcomes and the review in effect", () => {
		const html = renderToStaticMarkup(<ReviewTimeline data={passedRunReviews} testId="tl" />);
		expect(html).toMatch(/data-testid="tl-sealed"[^>]*data-outcome="fail"/);
		expect(html).toMatch(/data-testid="tl-effective"[^>]*data-outcome="pass"/);
		expect(html).toMatch(/data-testid="tl-entry-rv_synth0002"[^>]*data-state="current"/);
		expect(html).toContain("maya");
		expect(html).toContain("verifier is too strict");
		expect(html).toContain("grade.json failed_tests");
		expect(html).toContain("fail before, pass under this review");
	});

	it("keeps an older decision as superseded, oldest first", () => {
		const data: RunReviews = {
			...passedRunReviews,
			outcome: "invalid_evaluator",
			current: later,
			reviews: [...passedRunReviews.reviews, later],
		};
		const html = renderToStaticMarkup(<ReviewTimeline data={data} testId="tl" />);
		expect(html).toMatch(/data-testid="tl-entry-rv_synth0002"[^>]*data-state="superseded"/);
		expect(html).toMatch(/data-testid="tl-entry-rv_synth0003"[^>]*data-state="current"/);
		expect(html.indexOf("tl-entry-rv_synth0002")).toBeLessThan(html.indexOf("tl-entry-rv_synth0003"));
		expect(html).toContain("Replaces rv_synth0002.");
	});

	it("shows a flag without an outcome change", () => {
		const html = renderToStaticMarkup(<ReviewTimeline data={flaggedRunReviews} testId="tl" />);
		expect(html).toMatch(/data-testid="tl-entry-rv_synth0001"[^>]*data-state="flag"/);
		expect(html).not.toContain('data-testid="tl-change-rv_synth0001"');
	});

	it("says when there is no review and lists ignored ones apart", () => {
		const data: RunReviews = { ...flaggedRunReviews, reviews: [], problems: ["review rv_bad: content hash differs from the recorded one"] };
		const html = renderToStaticMarkup(<ReviewTimeline data={data} testId="tl" />);
		expect(html).toContain('data-testid="tl-empty"');
		expect(html).toContain('data-testid="tl-problems"');
		expect(html).toContain("content hash differs");
	});

	it("renders reason text inert", () => {
		const data: RunReviews = { ...passedRunReviews, reviews: [{ ...passedRunReviews.reviews[0], reason: "<script>alert(1)</script>" }] };
		const html = renderToStaticMarkup(<ReviewTimeline data={data} testId="tl" />);
		expect(html).not.toContain("<script>");
		expect(html).toContain("&lt;script&gt;");
	});
});