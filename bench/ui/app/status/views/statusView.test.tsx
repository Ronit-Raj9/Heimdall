import "@/lib/i18n";

import { errorQuery, loadingQuery, okQuery, render } from "@/lib/testing/render";
import { signedOutSession } from "@/lib/testing/platformFixtures";
import type { StatusPage } from "@/lib/store/apis/statusApi";
import { beforeEach, describe, expect, it, vi } from "vitest";
import StatusView, { StatusPageView } from "./statusView";

const api = vi.hoisted(() => ({ page: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/statusApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/statusApi")>()),
	useGetStatusPageQuery: () => api.page,
}));

const page: StatusPage = {
	level: "partial_outage",
	checked_at: "2026-11-22T10:00:00Z",
	version: "0.1.0",
	components: [
		{ id: "api", name: "Website and API", level: "operational" },
		{ id: "workers", name: "Evaluation workers", level: "partial_outage", detail: "some workers stopped answering", incident: "inc_1" },
		{ id: "gateway", name: "Model gateway", level: "unknown" },
	],
	incidents: [
		{
			id: "inc_1",
			title: "Evaluations start late",
			status: "identified",
			impact: "major",
			components: ["workers"],
			started_at: "2026-11-22T09:00:00Z",
			updates: [
				{ at: "2026-11-22T09:00:00Z", status: "investigating", message: "We are looking into it." },
				{ at: "2026-11-22T09:20:00Z", status: "identified", message: "A worker host ran out of disk." },
			],
		},
		{
			id: "inc_2",
			title: "Database upgrade",
			status: "scheduled",
			impact: "maintenance",
			started_at: "2026-11-22T08:00:00Z",
			scheduled_for: "2026-11-24T02:00:00Z",
			updates: [{ at: "2026-11-22T08:00:00Z", status: "scheduled", message: "Ten minutes of read-only time." }],
		},
	],
	recent: [],
};

beforeEach(() => {
	api.page = okQuery(page);
});

describe("StatusView", () => {
	it("shows the overall level, each component and the open incident newest note first", () => {
		const html = render(<StatusView />, signedOutSession());
		expect(html).toContain('data-level="partial_outage"');
		expect(html).toContain("Some parts are not working");
		expect(html).toContain('data-testid="status-component-workers"');
		expect(html).toContain("some workers stopped answering");
		expect(html).toContain("Partial outage");
		expect(html).toContain("No data");
		expect(html).toContain("Affects: Evaluation workers");
		expect(html.indexOf("ran out of disk")).toBeLessThan(html.indexOf("looking into it"));
		expect(html).toContain("Planned maintenance");
		expect(html).toContain("Affects the whole platform");
		expect(html).toContain("Nothing resolved in the last 14 days.");
		expect(html).not.toMatch(/runner|lease|probe|canary|sealed|\u2014/i);
	});

	it("reads all clear when everything is operational", () => {
		const html = render(
			<StatusPageView page={{ ...page, level: "operational", incidents: [], components: [{ id: "api", name: "Website and API", level: "operational" }] }} />,
		);
		expect(html).toContain("All systems operational");
		expect(html).toContain("No incidents right now.");
	});

	it("says so when the page cannot load, and while loading", () => {
		api.page = errorQuery(502, "bad gateway");
		expect(render(<StatusView />, signedOutSession())).toContain('data-testid="status-error"');
		api.page = loadingQuery();
		expect(render(<StatusView />, signedOutSession())).toContain('data-testid="status-loading"');
	});
});
