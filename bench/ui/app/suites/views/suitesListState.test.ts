import type { SuiteSummary } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { filtersFromUrl, filtersToUrl, readyShare, sortFromUrl, sortSuites } from "./suitesListState";

const s = (id: string, task_count: number, admitted_count: number) => ({ id, task_count, admitted_count }) as SuiteSummary;

describe("benchmarks list URL state (M6-43)", () => {
	it("reads filters back from the URL and drops unknown values", () => {
		expect(filtersFromUrl({ q: "swe", category: "coding", source: "team", sort: null })).toEqual({
			query: "swe",
			category: "coding",
			source: "team",
		});
		expect(filtersFromUrl({ q: null, category: "nope", source: "x", sort: null })).toEqual({ query: "", category: null, source: "all" });
	});

	it("writes only what differs from the defaults", () => {
		expect(filtersToUrl({ query: "", source: "all" })).toEqual({ q: null, source: null });
		expect(filtersToUrl({ query: "x", category: "memory" })).toEqual({ q: "x", category: "memory" });
	});

	it("sorts by name, tasks or ready share", () => {
		const list = [s("b-pack", 10, 1), s("a-pack", 5, 5), s("c-pack", 20, 0)];
		expect(sortFromUrl("bogus")).toBe("default");
		expect(sortSuites(list, "default").map((x) => x.id)).toEqual(["b-pack", "a-pack", "c-pack"]);
		expect(sortSuites(list, "name").map((x) => x.id)).toEqual(["a-pack", "b-pack", "c-pack"]);
		expect(sortSuites(list, "tasks").map((x) => x.id)).toEqual(["c-pack", "b-pack", "a-pack"]);
		expect(sortSuites(list, "ready").map((x) => x.id)).toEqual(["a-pack", "b-pack", "c-pack"]);
		expect(readyShare(s("x", 0, 0))).toBe(0);
	});
});
