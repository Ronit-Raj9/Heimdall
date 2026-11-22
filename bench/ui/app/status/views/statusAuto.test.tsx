import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { signedOutSession } from "@/lib/testing/platformFixtures";
import type { Incident, StatusPage } from "@/lib/store/apis/statusApi";
import { describe, expect, it, vi } from "vitest";
import { StatusPageView } from "./statusView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const auto: Incident & { auto?: boolean } = {
	id: "inc_auto",
	title: "Model gateway: not reachable",
	status: "investigating",
	impact: "critical",
	components: ["gateway"],
	started_at: "2026-11-22T09:00:00Z",
	updates: [{ at: "2026-11-22T09:00:00Z", status: "investigating", message: "Found by an automatic check." }],
	auto: true,
};

const operator: Incident = {
	id: "inc_op",
	title: "Evaluations start late",
	status: "identified",
	impact: "major",
	components: ["workers"],
	started_at: "2026-11-22T08:00:00Z",
	updates: [{ at: "2026-11-22T08:00:00Z", status: "identified", message: "A worker host ran out of disk." }],
};

const page: StatusPage = {
	level: "major_outage",
	checked_at: "2026-11-22T10:00:00Z",
	components: [
		{ id: "gateway", name: "Model gateway", level: "major_outage", detail: "not reachable", incident: "inc_auto" },
		{ id: "runs", name: "Evaluation runs", level: "operational" },
	],
	incidents: [auto, operator],
	recent: [{ ...auto, id: "inc_auto_old", status: "resolved", resolved_at: "2026-11-21T10:00:00Z" } as Incident],
};

describe("StatusPageView automatic incidents", () => {
	it("marks an automatic incident Found automatically, open or resolved, and leaves an operator's incident unmarked", () => {
		const html = render(<StatusPageView page={page} />, signedOutSession());
		expect(html).toContain('data-testid="status-incident-auto-inc_auto"');
		expect(html).toContain('data-testid="status-incident-auto-inc_auto_old"');
		expect(html).not.toContain('data-testid="status-incident-auto-inc_op"');
		expect(html).toContain("Found automatically");
		expect(html).toContain('data-testid="status-component-runs"');
	});
});