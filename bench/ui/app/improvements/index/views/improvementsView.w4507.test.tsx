import "@/lib/i18n";

import { IMPROVEMENT_FIXTURE } from "@/components/improvements/improve.fixtures";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ImprovementsTable } from "./improvementsView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("ImprovementsTable badges", () => {
	it.each([
		["supported", "Better"],
		["likely_better", "Likely better"],
		["insufficient_sample", "Not enough tasks"],
		["no_evidence", "No clear change"],
		["worse", "Worse"],
		["no_change", "No change"],
		["not_claimable", "Not claimable"],
	] as const)("shows the %s state as %s", (state, label) => {
		const html = renderToStaticMarkup(<ImprovementsTable rows={[{ ...IMPROVEMENT_FIXTURE, verdict: state }]} />);
		expect(html).toContain(`>${label}<`);
	});

	it("shows Teacher-assisted and Interrupted", () => {
		const html = renderToStaticMarkup(
			<ImprovementsTable rows={[{ ...IMPROVEMENT_FIXTURE, status: "interrupted", teacher: "claude-x" }]} />,
		);
		expect(html).toContain("Teacher-assisted");
		expect(html).toContain("Interrupted");
	});
});