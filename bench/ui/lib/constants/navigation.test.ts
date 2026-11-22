import "@/lib/i18n";

import { setPageTitleOverride } from "@/lib/routing/pageTitle";
import { describe, expect, it } from "vitest";
import { BENCH_NAV, BENCH_NAV_GROUPS, DEFAULT_CLOSED_GROUPS, activeNavGroup, activeNavItem, pageTitle } from "./navigation";

describe("navigation", () => {
	it("is agent first: Home, then Agents, Test, Tasks, Watch, Release and one Settings entry", () => {
		expect(BENCH_NAV_GROUPS.map((group) => group.id)).toEqual(["home", "agents", "test", "tasks", "watch", "release", "settings"]);
		expect(BENCH_NAV_GROUPS[0].labelKey).toBeUndefined();
		expect(DEFAULT_CLOSED_GROUPS).toEqual([]);
		expect(BENCH_NAV.map((item) => item.to)).toEqual([
			"/",
			"/agents",
			"/leaderboard",
			"/improvements",
			"/evaluations",
			"/experiments",
			"/failure-tests",
			"/mcp-test",
			"/runs",
			"/suites",
			"/annotation",
			"/why-agents-fail",
			"/scheduled-checks",
			"/live",
			"/releases",
			"/settings",
		]);
		expect(BENCH_NAV_GROUPS.find((group) => group.id === "settings")?.items).toHaveLength(1);
	});

	it("never lists a start path other than /evaluate, nor the retired pages", () => {
		const routes = BENCH_NAV.map((item) => item.to as string);
		for (const gone of [
			"/welcome",
			"/quickstart",
			"/experiments/new",
			"/catalog",
			"/canaries",
			"/schedules",
			"/review",
			"/policies",
			"/models",
			"/workers",
			"/jobs",
			"/docs",
			"/admin",
		]) {
			expect(routes).not.toContain(gone);
		}
	});

	it("highlights the owning entry on nested routes and on old routes that became tabs", () => {
		const cases: [string, string | null][] = [
			["/", "/"],
			["/welcome", "/"],
			["/quickstart", "/"],
			["/evaluations", "/evaluations"],
			["/evaluations/ev_1", "/evaluations"],
			["/evaluate", "/evaluations"],
			["/experiments", "/experiments"],
			["/experiments/e_1", "/experiments"],
			["/experiments/e_1/", "/experiments"],
			["/experiments/new", "/experiments"],
			["/verdict", "/experiments"],
			["/failure-tests", "/failure-tests"],
			["/mcp-test", "/mcp-test"],
			["/runs/r_1", "/runs"],
			["/compare", "/runs"],
			["/agents/diff", "/agents"],
			["/agents_/diff", "/agents"],
			["/suites/coding-core", "/suites"],
			["/catalog", "/suites"],
			["/imports", "/suites"],
			["/annotation", "/annotation"],
			["/annotation/q_1", "/annotation"],
			["/trends", "/leaderboard"],
			["/why-agents-fail", "/why-agents-fail"],
			["/clusters", "/why-agents-fail"],
			["/triage", "/why-agents-fail"],
			["/canaries", "/scheduled-checks"],
			["/schedules", "/scheduled-checks"],
			["/review", "/releases"],
			["/policies", "/releases"],
			["/settings", "/settings"],
			["/models", "/settings"],
			["/workers", "/settings"],
			["/jobs/j_1", "/settings"],
			["/admin", "/settings"],
			["/docs", null],
			["/runsx", null],
		];
		for (const [path, want] of cases) expect(activeNavItem(path)?.to ?? null, path).toBe(want);
	});

	it("names the group of the current entry", () => {
		expect(activeNavGroup("/")).toBe("home");
		expect(activeNavGroup("/agents/a1")).toBe("agents");
		expect(activeNavGroup("/experiments")).toBe("test");
		expect(activeNavGroup("/catalog")).toBe("tasks");
		expect(activeNavGroup("/live")).toBe("watch");
		expect(activeNavGroup("/policies")).toBe("release");
		expect(activeNavGroup("/jobs/j_1")).toBe("settings");
	});

	// N139, N121: the guides and the jobs pages have no sidebar entry of their own, so they name themselves.
	it("titles the guides, the jobs list and a job page by themselves", () => {
		expect(pageTitle("/docs")).toBe("Guides · Heimdall");
		expect(pageTitle("/jobs")).toBe("Jobs · Heimdall");
		expect(pageTitle("/jobs/job_1")).toBe("Job · Heimdall");
		expect(pageTitle("/settings")).toBe("Settings · Heimdall");
	});

	it("keeps a title the page set for itself", () => {
		setPageTitleOverride("/docs", "Hosting · Guides · Heimdall");
		expect(pageTitle("/docs")).toBe("Hosting · Guides · Heimdall");
		expect(pageTitle("/docs/")).toBe("Hosting · Guides · Heimdall");
		setPageTitleOverride("/docs", undefined);
		expect(pageTitle("/docs")).toBe("Guides · Heimdall");
	});
});