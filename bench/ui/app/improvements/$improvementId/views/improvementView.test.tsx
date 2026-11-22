import "@/lib/i18n";

import { errorQuery, mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import ImprovementView from "./improvementView";

const api = vi.hoisted(() => ({ one: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/improveApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/improveApi")>()),
	useGetImprovementQuery: () => api.one,
	useApproveImprovementRoundMutation: () => mutation(),
	useStopImprovementMutation: () => mutation(),
}));

describe("ImprovementView", () => {
	it("says an unknown run does not exist, with a link back and no Retry", () => {
		api.one = errorQuery(404, "improvement not found: zz-nope");
		const html = render(<ImprovementView id="zz-nope" />);
		expect(html).toContain("No improvement run named zz-nope.");
		expect(html).toContain('href="/improvements"');
		expect(html).not.toContain("improvement-error-retry");
	});

	it("keeps Retry for a failure that is not a 404", () => {
		api.one = errorQuery(500, "disk full");
		const html = render(<ImprovementView id="r1" />);
		expect(html).toContain("improvement-error-retry");
	});
});