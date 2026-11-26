import "@/lib/i18n";

import type { ExperimentValidity } from "@/lib/store/apis/validityApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { FlipBannerView } from "./flipBanner";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const flipped: ExperimentValidity = {
	entries: [
		{
			id: "e_1:rg_1",
			exp_id: "e_1",
			regrade_id: "rg_1",
			note: "The test check was fixed.",
			before: { source: "original", decision: "invalid" },
			after: { source: "rg_1", decision: "inconclusive" },
			flipped: true,
			at: "2026-11-23T09:00:00Z",
		},
	],
	stale: null,
};

describe("flip banner", () => {
	it("says the 23 Nov re-score turned a broken test into no clear winner", () => {
		const html = renderToStaticMarkup(<FlipBannerView validity={flipped} />);
		expect(html).toContain("This result changed after a re-score on 23 Nov");
		expect(html).toContain("Test was broken");
		expect(html).toContain("No clear winner yet");
		expect(html).toContain("The test check was fixed.");
		expect(html).toContain('href="/validity"');
		expect(html).toContain("Got it");
	});

	it("shows the stale note when nothing flipped and a check was updated", () => {
		const html = renderToStaticMarkup(
			<FlipBannerView
				validity={{ entries: [], stale: { exp_id: "e_1", gates: { protected_files: ["1", "2"] }, checked: "2026-11-27T00:00:00Z" } }}
			/>,
		);
		expect(html).toContain("A check this comparison used has been updated since it ran.");
	});

	it("shows nothing for an unchanged comparison", () => {
		expect(renderToStaticMarkup(<FlipBannerView validity={{ entries: [], stale: null }} />)).toBe("");
	});
});