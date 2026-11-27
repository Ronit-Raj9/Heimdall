import type { SuiteTask } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { pageCount, pageOfTask, searchTasks, taskPage } from "./taskPaging";

const task = (id: string, title = id) => ({ id, title }) as unknown as SuiteTask;
const many = Array.from({ length: 3641 }, (_, i) => task(`bfcl-irrelevance-${i}`, `Task ${i}`));

describe("taskPage", () => {
	it("shows 50 rows of 3641 on page 1", () => {
		const page = taskPage(many, null, null);
		expect(page.rows).toHaveLength(50);
		expect([page.first, page.last, page.total, page.pages]).toEqual([1, 50, 3641, 73]);
	});
	it("page 2 is rows 51 to 100", () => {
		const page = taskPage(many, 2, null);
		expect([page.first, page.last]).toEqual([51, 100]);
		expect(page.rows[0].id).toBe("bfcl-irrelevance-50");
	});
	it("the last page holds the remainder and a page past the end is clamped", () => {
		expect(taskPage(many, 73, null).rows).toHaveLength(41);
		expect(taskPage(many, 999, null).page).toBe(73);
		expect(taskPage(many, 0, null).page).toBe(1);
	});
	it("opens the page of the linked task when the URL has no page", () => {
		const page = taskPage(many, null, "bfcl-irrelevance-900");
		expect(page.page).toBe(19);
		expect(page.rows.some((t) => t.id === "bfcl-irrelevance-900")).toBe(true);
	});
	it("an explicit page wins over the linked task", () => {
		expect(taskPage(many, 3, "bfcl-irrelevance-900").page).toBe(3);
	});
	it("an empty list is page 1 of 1 with no rows", () => {
		expect(taskPage([], null, null)).toMatchObject({ page: 1, pages: 1, first: 0, last: 0, total: 0 });
	});
});

describe("searchTasks", () => {
	it("matches id and title, case-insensitive", () => {
		expect(searchTasks(many, "IRRELEVANCE-10").map((t) => t.id)).toContain("bfcl-irrelevance-10");
		expect(searchTasks(many, "irrelevance-10").length).toBe(111);
		expect(searchTasks([task("a", "Fix the parser")], "parser")).toHaveLength(1);
	});
	it("an empty or blank search keeps all", () => {
		expect(searchTasks(many, null)).toBe(many);
		expect(searchTasks(many, "  ")).toBe(many);
	});
});

describe("pageCount and pageOfTask", () => {
	it("counts pages and finds a task", () => {
		expect(pageCount(0)).toBe(1);
		expect(pageCount(51)).toBe(2);
		expect(pageOfTask(many, "missing")).toBeNull();
		expect(pageOfTask(many, null)).toBeNull();
	});
});
