import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReviewedBadge } from "./reviewedBadge";

describe("ReviewedBadge", () => {
	it("renders nothing under another grade source with no reviewed run", () => {
		expect(renderToStaticMarkup(<ReviewedBadge gradeSource="original" testId="b" />)).toBe("");
		expect(renderToStaticMarkup(<ReviewedBadge reviewed={0} gradeSource="rg_1" testId="b" />)).toBe("");
	});

	it("counts reviewed and changed runs and never says verified", () => {
		const html = renderToStaticMarkup(<ReviewedBadge reviewed={3} changed={2} gradeSource="reviewed" testId="b" />);
		expect(html).toContain('data-testid="b"');
		expect(html).toContain("3 runs human-reviewed");
		expect(html).toContain("2 changed");
		expect(html).toContain("never verified");
		expect(html).not.toMatch(/>[^<]*\bverified\b[^<]*</i);
	});

	it("uses the singular for one run and hides a zero change count", () => {
		const html = renderToStaticMarkup(<ReviewedBadge reviewed={1} changed={0} gradeSource="reviewed" testId="b" />);
		expect(html).toContain("1 run human-reviewed");
		expect(html).not.toMatch(/>[^<]*changed[^<]*</);
	});

	it("says when grade source reviewed applied no review", () => {
		const html = renderToStaticMarkup(<ReviewedBadge gradeSource="reviewed" testId="b" />);
		expect(html).toContain("no review applied");
	});
});