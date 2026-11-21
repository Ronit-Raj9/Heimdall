import { describe, expect, it } from "vitest";
import { BENCH_NAV, BENCH_NAV_GROUPS, DEFAULT_CLOSED_GROUPS, activeNavGroup, activeNavItem } from "./navigation";

describe("navigation", () => {
	it("has the six groups of the information architecture, in order, with Advanced closed by default", () => {
		expect(BENCH_NAV_GROUPS.map((group) => group.id)).toEqual(["start", "benchmark", "build", "production", "govern", "operate"]);
		expect(DEFAULT_CLOSED_GROUPS).toEqual(["operate"]);
		expect(BENCH_NAV.map((item) => item.to)).toEqual([
			"/",
			"/welcome",
			"/evaluations",
			"/experiments",
			"/evaluations",
			"/runs",
			"/leaderboard",
			"/agents",
			"/suites",
			"/catalog",
			"/models",
			"/files",
			"/canaries",
			"/live",
			"/inbox",
			"/schedules",
			"/review",
			"/releases",
			"/monitors",
			"/policies",
			"/workers",
			"/settings",
			"/jobs",
			"/integrations",
			"/operate",
			"/usage",
			"/upload",
			"/docs",
			"/admin",
		]);
	});

	it("never lists New experiment or the quickstart as entries", () => {
		expect(BENCH_NAV.some((item) => item.to.startsWith("/experiments/new"))).toBe(false);
		expect(BENCH_NAV.some((item) => (item.to as string) === "/quickstart")).toBe(false);
	});

	it("highlights the object's entry on nested routes", () => {
		const cases: [string, string | null][] = [
			["/", "/"],
			["/welcome", "/welcome"],
			["/quickstart", "/"],
			["/evaluations", "/evaluations"],
			["/evaluations/ev_1", "/evaluations"],
			["/evaluate", "/evaluations"],
			["/experiments", "/experiments"],
			["/experiments/e_1", "/experiments"],
			["/experiments/e_1/", "/experiments"],
			["/experiments/new", "/experiments"],
			["/verdict", "/experiments"],
			["/evaluations/ev_1", "/evaluations"],
			["/runs/r_1", "/runs"],
			["/compare", "/runs"],
			["/agents/diff", "/agents"],
			["/agents_/diff", "/agents"],
			["/suites/coding-core", "/suites"],
			["/imports", "/suites"],
			["/trends", "/leaderboard"],
			["/clusters", "/inbox"],
			["/triage", "/inbox"],
			["/settings", "/settings"],
			["/jobs/j_1", "/jobs"],
			["/runsx", null],
		];
		for (const [path, want] of cases) expect(activeNavItem(path)?.to ?? null, path).toBe(want);
	});

	it("names the group of the current entry", () => {
		expect(activeNavGroup("/")).toBe("start");
		expect(activeNavGroup("/inbox")).toBe("production");
		expect(activeNavGroup("/workers")).toBe("operate");
		expect(activeNavGroup("/jobs/j_1")).toBe("operate");
	});
});