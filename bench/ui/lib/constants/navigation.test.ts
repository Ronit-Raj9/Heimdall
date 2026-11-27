import "@/lib/i18n";

import { setPageTitleOverride } from "@/lib/routing/pageTitle";
import { describe, expect, it } from "vitest";
import { BENCH_NAV, BENCH_NAV_GROUPS, DEFAULT_CLOSED_GROUPS, activeNavGroup, activeNavItem, pageTitle } from "./navigation";

describe("navigation", () => {
	it("follows Spec 1: Home, Test, Results, Benchmarks, Production, then a bottom group", () => {
		expect(BENCH_NAV_GROUPS.map((group) => group.id)).toEqual(["home", "test", "results", "benchmarks", "production", "bottom"]);
		expect(BENCH_NAV_GROUPS[0].labelKey).toBeUndefined();
		expect(BENCH_NAV_GROUPS.at(-1)?.labelKey).toBeUndefined();
		expect(DEFAULT_CLOSED_GROUPS).toEqual([]);
		expect(BENCH_NAV.map((item) => item.to)).toEqual([
			"/",
			"/agents",
			"/evaluations",
			"/experiments",
			"/failure-tests",
			"/mcp-test",
			"/runs",
			"/why-agents-fail",
			"/leaderboard",
			"/releases",
			"/suites",
			"/annotation",
			"/inbox",
			"/live",
			"/scheduled-checks",
			"/gateway",
			"/monitors",
			"/improvements",
			"/settings",
			"/docs",
		]);
		expect(BENCH_NAV.find((item) => item.to === "/improvements")?.adminOnly).toBe(true);
	});

	it("never lists a start path other than /evaluate, nor the retired pages", () => {
		const routes = BENCH_NAV.map((item) => item.to as string);
		for (const gone of [
			"/welcome",
			"/experiments/new",
			"/catalog",
			"/canaries",
			"/schedules",
			"/review",
			"/policies",
			"/models",
			"/workers",
			"/jobs",
			"/quickstart",
			"/admin",
		]) {
			expect(routes).not.toContain(gone);
		}
	});

	it("highlights the owning entry on nested routes and on old routes that became tabs", () => {
		const cases: [string, string | null][] = [
			["/", "/"],
			["/welcome", "/"],
			["/quickstart", null],
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
			["/docs", "/docs"],
			["/inbox", "/inbox"],
			["/maxim", "/inbox"],
			["/gateway", "/gateway"],
			["/replays", "/experiments"],
			["/validity", "/releases"],
			["/upload", "/suites"],
			["/runsx", null],
		];
		for (const [path, want] of cases) expect(activeNavItem(path)?.to ?? null, path).toBe(want);
	});

	it("names the group of the current entry", () => {
		expect(activeNavGroup("/")).toBe("home");
		expect(activeNavGroup("/agents/a1")).toBe("test");
		expect(activeNavGroup("/experiments")).toBe("test");
		expect(activeNavGroup("/runs/r_1")).toBe("results");
		expect(activeNavGroup("/catalog")).toBe("benchmarks");
		expect(activeNavGroup("/live")).toBe("production");
		expect(activeNavGroup("/policies")).toBe("results");
		expect(activeNavGroup("/jobs/j_1")).toBe("bottom");
	});

	it("uses the search where one path belongs to two entries", () => {
		expect(activeNavItem("/compare")?.to).toBe("/runs");
		expect(activeNavItem("/compare", { exp: "e_1" })?.to).toBe("/experiments");
		expect(activeNavItem("/experiments/e_1", { from: "eval" })?.to).toBe("/evaluations");
		expect(activeNavItem("/runs/r_1", { from: "eval" })?.to).toBe("/evaluations");
	});

	// N139, N121: the guides and the jobs pages have no sidebar entry of their own, so they name themselves.
	it("titles the guides, the jobs list and a job page by themselves", () => {
		expect(pageTitle("/docs")).toBe("Guides · Heimdall");
		expect(pageTitle("/jobs")).toBe("Background jobs · Heimdall");
		expect(pageTitle("/jobs/job_1")).toBe("Background job · Heimdall");
	});

	// Spec 1 title rule: a tabbed page names its tab, and the unlinked pages name themselves.
	it("titles Settings tabs and the pages that had no title of their own", () => {
		expect(pageTitle("/settings")).toBe("Model keys · Settings · Heimdall");
		expect(pageTitle("/settings", { tab: "secrets" })).toBe("Secrets · Settings · Heimdall");
		expect(pageTitle("/settings", { tab: "nope" })).toBe("Settings · Heimdall");
		expect(pageTitle("/gateway")).toBe("Gateway report card · Heimdall");
		expect(pageTitle("/validity")).toBe("Changed results · Heimdall");
		expect(pageTitle("/replays")).toBe("Replay on a new model · Heimdall");
		expect(pageTitle("/inbox")).toBe("Production failures · Heimdall");
		expect(pageTitle("/evaluate")).toBe("Evaluate an agent · Heimdall");
		expect(pageTitle("/runs")).toBe("Runs · Heimdall");
	});

	it("keeps a title the page set for itself", () => {
		setPageTitleOverride("/docs", "Hosting · Guides · Heimdall");
		expect(pageTitle("/docs")).toBe("Hosting · Guides · Heimdall");
		expect(pageTitle("/docs/")).toBe("Hosting · Guides · Heimdall");
		setPageTitleOverride("/docs", undefined);
		expect(pageTitle("/docs")).toBe("Guides · Heimdall");
	});
});