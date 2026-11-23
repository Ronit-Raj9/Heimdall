import "@/lib/i18n";

import type { AdaptiveView } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { TrialsPanel } from "./trialsPanel";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

// Fix wave 3, N246: a follow-up's trials panel links the comparison it extends.
describe("trials panel follow-up link", () => {
	it("links the parent comparison", () => {
		const view: AdaptiveView = {
			exp_id: "e_ext",
			tier: "",
			tier_tasks: [],
			trials: null,
			plan: null,
			report: null,
			more_evidence: null,
			extends: { exp_id: "e_zz_test_c10_matrix_gsz9", manifest_hash: "h", kind: "trials", rule: "exploratory" },
			decision: "",
			max_slots: 0,
		};
		const html = renderToStaticMarkup(<TrialsPanel view={view} />);
		expect(html).toMatch(/<a href="\/experiments\/e_zz_test_c10_matrix_gsz9"[^>]*data-testid="trials-extends"/);
	});
});