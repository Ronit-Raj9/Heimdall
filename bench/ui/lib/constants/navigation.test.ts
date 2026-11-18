import { describe, expect, it } from "vitest";
import { BENCH_NAV, BENCH_NAV_GROUPS, activeNavGroup, activeNavItem } from "./navigation";

describe("navigation", () => {
	it("has the five groups of the information architecture, in order", () => {
		expect(BENCH_NAV_GROUPS.map((group) => group.id)).toEqual(["benchmark", "build", "production", "govern", "operate"]);
		expect(BENCH_NAV.map((item) => item.to)).toEqual([
			"/",
			"/experiments",
			"/runs",
			"/leaderboard",
			"/agents",
			"/suites",
			"/canaries",
			"/live",
			"/inbox",
			"/review",
			"/releases",
			"/monitors",
			"/workers",
			"/settings",
		]);
	});

	it("never lists New experiment or the quickstart as entries", () => {
		expect(BENCH_NAV.some((item) => item.to.startsWith("/experiments/new"))).toBe(false);
		expect(BENCH_NAV.some((item) => (item.to as string) === "/quickstart")).toBe(false);
	});

	it("highlights the object's entry on nested routes", () => {
		const cases: [string, string | null][] = [
			["/", "/"],
			["/quickstart", "/"],
			["/experiments", "/experiments"],
			["/experiments/e_1", "/experiments"],
			["/experiments/e_1/", "/experiments"],
			["/experiments/new", "/experiments"],
			["/verdict", "/experiments"],
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
			["/jobs/j_1", null],
			["/runsx", null],
		];
		for (const [path, want] of cases) expect(activeNavItem(path)?.to ?? null, path).toBe(want);
	});

	it("names the group of the current entry", () => {
		expect(activeNavGroup("/inbox")).toBe("production");
		expect(activeNavGroup("/workers")).toBe("operate");
		expect(activeNavGroup("/jobs/j_1")).toBeNull();
	});
});