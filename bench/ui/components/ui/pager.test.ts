import { describe, expect, it } from "vitest";
import { PAGE_SIZE, pageOfIndex, pageSlice, pageWindow, priorityClass } from "@/components/ui/pager";

describe("pager helpers (Spec 10)", () => {
	it("slices 50 rows and clamps the page", () => {
		const rows = Array.from({ length: 3641 }, (_, i) => i);
		const p2 = pageSlice(rows, 2);
		expect(p2.rows).toHaveLength(PAGE_SIZE);
		expect(p2.first).toBe(51);
		expect(p2.last).toBe(100);
		expect(p2.pageCount).toBe(73);
		expect(pageSlice(rows, 999).page).toBe(73);
		expect(pageSlice(rows, 999).last).toBe(3641);
		expect(pageSlice(rows, 0).page).toBe(1);
	});
	it("handles an empty list", () => {
		expect(pageWindow(0, 1)).toMatchObject({ page: 1, pageCount: 1, first: 0, last: 0 });
	});
	it("finds the page of a row", () => {
		expect(pageOfIndex(0)).toBe(1);
		expect(pageOfIndex(49)).toBe(1);
		expect(pageOfIndex(50)).toBe(2);
		expect(pageOfIndex(-1)).toBe(1);
	});
	it("hides only secondary columns on phones", () => {
		expect(priorityClass("secondary")).toBe("max-sm:hidden");
		expect(priorityClass("status")).toBe("");
		expect(priorityClass(undefined)).toBe("");
	});
});
