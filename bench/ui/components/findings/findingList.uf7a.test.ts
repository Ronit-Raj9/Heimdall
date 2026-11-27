import type { Finding } from "@/lib/store/apis/findingsApi";
import { describe, expect, it } from "vitest";
import { filterFindings, findingHref } from "./findingList";

const f = (id: string, title: string, task: string) =>
	({
		id,
		title,
		tasks: [{ suite: "s", task }],
		history: [],
	}) as unknown as Finding;

describe("findings list helpers", () => {
	it("links each finding to its own URL", () => {
		expect(findingHref("f 1")).toBe("?tab=findings&finding=f%201");
	});
	it("narrows by title, id or task", () => {
		const list = [f("a", "Skips the tests", "t01"), f("b", "Times out", "t02")];
		expect(filterFindings(list, "t02").map((x) => x.id)).toEqual(["b"]);
		expect(filterFindings(list, "skips").map((x) => x.id)).toEqual(["a"]);
		expect(filterFindings(list, "")).toHaveLength(2);
	});
});