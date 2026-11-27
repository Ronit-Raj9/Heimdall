import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { IMPROVEMENT_FIXTURE } from "./improve.fixtures";
import { ImprovementDetailBody } from "./improvementDetail";

vi.mock("@/lib/store/apis/improveApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/improveApi")>()),
	useGetVersionExplainQuery: () => ({ isLoading: false, data: undefined, error: undefined }),
}));

describe("ImprovementDetailBody", () => {
	it("renders the verdict, curve with band and control, rounds, lineage, integrity and spend", () => {
		const html = renderToStaticMarkup(<ImprovementDetailBody data={IMPROVEMENT_FIXTURE} />);
		for (const id of [
			"improvement-verdict",
			"improvement-curve",
			"improvement-band",
			"improvement-null",
			"improvement-line-sealed",
			"improvement-marker-committed",
			"improvement-round-1",
			"improvement-round-2",
			"improvement-file-diff",
			"improvement-lineage-tree",
			"improvement-chain",
			"improvement-integrity-strip",
			"improvement-cost",
		]) {
			expect(html).toContain(`data-testid="${id}"`);
		}
		expect(html).toContain("Improvement supported");
		expect(html).toContain("Held-out tasks");
		expect(html).toContain("Stopped logging");
		expect(html).toContain("Approved by bo: logging change reviewed");
		expect(html).toContain("+20.0 pts");
		expect(html).not.toContain("improvement-approve-form");
		// Plain words only: no internal jargon in what users read.
		const text = html.replace(/<[^>]+>/g, " ").toLowerCase();
		for (const banned of ["canary", "sealed", "arm ", "probe", "suite", "—"]) {
			expect(text).not.toContain(banned);
		}
	});

	it("shows the approval form on a paused round", () => {
		const paused = { ...IMPROVEMENT_FIXTURE, status: "awaiting_approval" as const, verdict_detail: undefined };
		const html = renderToStaticMarkup(<ImprovementDetailBody data={paused} onApprove={() => {}} />);
		expect(html).toContain('data-testid="improvement-approve-form"');
		expect(html).toContain("Still running");
	});
});