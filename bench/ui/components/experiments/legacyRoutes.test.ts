import { describe, expect, it } from "vitest";
import { compareHref, compareSearch, legacyExperimentTarget, legacyRunsTarget, stringSearch } from "./legacyRoutes";

// The redirect table of the UI regroup (U2): every old URL lands on the experiment tab or page that replaced it.
describe("legacyExperimentTarget", () => {
	const cases = [
		{ path: "/verdict", search: { exp: "e_x" }, want: { to: "/experiments/$expId", params: { expId: "e_x" }, search: {} } },
		{
			path: "/verdict",
			search: { exp: "e_x", grade: "reviewed" },
			want: { to: "/experiments/$expId", params: { expId: "e_x" }, search: { grade: "reviewed" } },
		},
		{ path: "/verdict", search: {}, want: { to: "/experiments" } },
		{
			path: "/receipts",
			search: { exp: "e_x", condition: "stale-notes", run: "r_1" },
			want: { to: "/experiments/$expId", params: { expId: "e_x" }, search: { tab: "receipts", condition: "stale-notes", run: "r_1" } },
		},
		{ path: "/receipts", search: {}, want: { to: "/experiments" } },
		{
			path: "/matrix",
			search: { exp: "e_x", alpha: 0.05, condition: "clean" },
			want: { to: "/experiments/$expId", params: { expId: "e_x" }, search: { tab: "matrix", alpha: "0.05", condition: "clean" } },
		},
		{ path: "/matrix", search: { condition: "clean" }, want: { to: "/experiments" } },
		{
			path: "/migration",
			search: { exp: "e_x", from: "A", to: "B,C" },
			want: { to: "/experiments/$expId", params: { expId: "e_x" }, search: { tab: "migration", from: "A", to: "B,C" } },
		},
		{ path: "/migration", search: {}, want: { to: "/experiments" } },
	] as const;
	for (const c of cases) {
		it(`${c.path} ${JSON.stringify(c.search)}`, () => {
			expect(legacyExperimentTarget(c.path, c.search)).toEqual(c.want);
		});
	}
});

describe("legacyRunsTarget", () => {
	it("sends the old pair view to /compare with every parameter", () => {
		expect(legacyRunsTarget({ pair: "p_1", exp: "e_x" })).toEqual({ to: "/compare", search: { pair: "p_1", exp: "e_x" } });
	});
	it("leaves the run list alone without a pair", () => {
		expect(legacyRunsTarget({})).toBeNull();
		expect(legacyRunsTarget({ exp: "e_x", arm: "C" })).toBeNull();
	});
});

describe("compare links", () => {
	it("build the pair comparison URL", () => {
		expect(compareHref("p_1", "e_x")).toBe("/compare?pair=p_1&exp=e_x");
		expect(compareHref("p_1")).toBe("/compare?pair=p_1");
		expect(compareSearch(undefined, "e_x")).toEqual({ exp: "e_x" });
	});
	it("turn parsed search values back into strings and drop empty ones", () => {
		expect(stringSearch({ a: 1, b: "x", c: "", d: null, e: true })).toEqual({ a: "1", b: "x", e: "true" });
	});
});

// 6-A-01: a stale tab in an old bookmark never overrides the page the bookmark names.
describe("legacyExperimentTarget with a stale tab", () => {
	it("keeps the legacy page's tab", () => {
		expect(legacyExperimentTarget("/receipts", { exp: "e1", tab: "cost" }).search).toEqual({ tab: "receipts" });
		expect(legacyExperimentTarget("/matrix", { exp: "e1", tab: "runs", run: "r1" }).search).toEqual({ tab: "matrix", run: "r1" });
	});
	it("drops the stale tab for the verdict default", () => {
		expect(legacyExperimentTarget("/verdict", { exp: "e1", tab: "cost" }).search).toEqual({});
	});
});

