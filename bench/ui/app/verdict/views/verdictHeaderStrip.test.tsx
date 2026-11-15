import "@/lib/i18n";

import { verdictFixture } from "@/components/verdict/testFixtures";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { VerdictHeaderStrip } from "./verdictHeaderStrip";

describe("VerdictHeaderStrip", () => {
	it("shows mode, short manifest hash, frozen time, suite and grade source", () => {
		const html = renderToStaticMarkup(<VerdictHeaderStrip payload={verdictFixture} />);
		expect(html).toContain('data-testid="mode-badge-synthetic"');
		expect(html).toContain(">52b5454d…9338<");
		expect(html).toContain("sha256:52b5454d8f01de367a0a3baf9d55499ffab1b0b0343bd4585e4cc1c962fc9338");
		expect(html).toContain('data-testid="verdict-frozen-at"');
		expect(html).toContain(">coding-core@1.0.0<");
		expect(html).toContain(">original<");
	});

	it("shows a regrade id as the grade source", () => {
		const html = renderToStaticMarkup(<VerdictHeaderStrip payload={{ ...verdictFixture, grade_source: "g_regrade01" }} />);
		expect(html).toContain(">g_regrade01<");
		expect(html).not.toContain('data-testid="verdict-reviewed-badge"');
	});

	it("counts human-reviewed runs under grade source reviewed", () => {
		const html = renderToStaticMarkup(
			<VerdictHeaderStrip payload={{ ...verdictFixture, grade_source: "reviewed", reviewed_runs: 2, review_changed_runs: 1 }} />,
		);
		expect(html).toContain(">reviewed<");
		expect(html).toMatch(/data-testid="verdict-reviewed-badge"[^>]*data-reviewed="2"/);
		expect(html).toContain("2 runs human-reviewed");
	});
});