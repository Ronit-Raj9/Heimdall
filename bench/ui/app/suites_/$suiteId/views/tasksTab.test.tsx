import "@/lib/i18n";

import type { SuiteSummary } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { revealOpenTask, SuiteTasksTab } from "./tasksTab";

// N163: a task opened from a link (?task=) is shown first and scrolled into view; a task id the
// suite does not have is named in a note instead of being dropped silently.

const query = vi.hoisted(() => ({ q: {} as Record<string, string | null> }));
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryStates: () => [query.q, () => Promise.resolve(new URLSearchParams())],
}));
vi.mock("@/components/suites/suiteTaskFilters", () => ({ SuiteTaskFilters: () => null }));
vi.mock("@/components/suites/suiteTaskTable", () => ({ SuiteTaskTable: () => <table data-testid="task-table-stub" /> }));
vi.mock("@/components/suites/taskCategoryCell", () => ({ useTaskCategories: () => ({ labels: undefined, canEdit: false }) }));
vi.mock("@/components/suites/taskDetailPanel", () => ({
	TaskDetailPanel: ({ taskId }: { taskId: string }) => <div data-testid={`task-detail-stub-${taskId}`} />,
}));

const task = (id: string) => ({ id, title: id, checks: [], capabilities: [] });
const suite = { id: "hd-harbor-bfcl", version: "1.0.0", tasks: [task("t1"), task("t2")] } as unknown as SuiteSummary;
const health = { state: "loading" } as never;

describe("SuiteTasksTab opened from a link", () => {
	it("renders the linked task's panel before the table", () => {
		query.q = { task: "t2" };
		const html = renderToStaticMarkup(<SuiteTasksTab suite={suite} health={health} />);
		const panel = html.indexOf('data-testid="task-detail-stub-t2"');
		const table = html.indexOf('data-testid="task-table-stub"');
		expect(panel).toBeGreaterThan(-1);
		expect(table).toBeGreaterThan(-1);
		expect(panel).toBeLessThan(table);
		expect(html).not.toContain('data-testid="suites-task-missing"');
	});

	it("names the version the run used when the link sends ?rev= (M1-12)", () => {
		query.q = { task: "t2", rev: "0.9.0" };
		const html = renderToStaticMarkup(<SuiteTasksTab suite={suite} health={health} />);
		expect(html).toContain('data-testid="suites-task-used-version"');
		expect(html).toContain("The run used version 0.9.0 of this benchmark.");
		query.q = { task: "t2" };
		expect(renderToStaticMarkup(<SuiteTasksTab suite={suite} health={health} />)).not.toContain("suites-task-used-version");
	});

	it("names a task the benchmark does not have", () => {
		query.q = { task: "gone-task" };
		const html = renderToStaticMarkup(<SuiteTasksTab suite={suite} health={health} />);
		expect(html).toContain('data-testid="suites-task-missing"');
		expect(html).toContain("Task gone-task is not in version 1.0.0 of this benchmark.");
		expect(html).not.toContain("task-detail-stub");
	});

	it("scrolls the panel and the selected row into view once per task", () => {
		const panel = { scrollIntoView: vi.fn() };
		const row = { scrollIntoView: vi.fn() };
		const seen = { current: null as string | null };
		revealOpenTask("t2", seen, panel, row);
		revealOpenTask("t2", seen, panel, row);
		expect(panel.scrollIntoView).toHaveBeenCalledTimes(1);
		expect(panel.scrollIntoView).toHaveBeenCalledWith({ block: "start" });
		expect(row.scrollIntoView).toHaveBeenCalledTimes(1);
		expect(row.scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
		revealOpenTask(null, seen, panel, row);
		expect(panel.scrollIntoView).toHaveBeenCalledTimes(1);
	});
});

describe("SuiteTasksTab paging (M6-1)", () => {
	const big = {
		id: "hd-harbor-bfcl",
		version: "1.0.0",
		tasks: Array.from({ length: 3641 }, (_, i) => task(`bfcl-${i}`)),
	} as unknown as SuiteSummary;
	it("counts the page of 50 rows and has no second Tasks heading", () => {
		query.q = {};
		const html = renderToStaticMarkup(<SuiteTasksTab suite={big} health={health} />);
		expect(html).toContain("1 to 50 of 3641 tasks");
		expect(html).toContain('data-testid="suites-tasks-next"');
		// The shared Pager (S2) with the page's own range text.
		expect(html).toContain("Page 1 of 73");
		expect(html).not.toContain("<h2");
	});
	it("page 2 reads 51 to 100", () => {
		query.q = { page: 2 as unknown as string };
		const html = renderToStaticMarkup(<SuiteTasksTab suite={big} health={health} />);
		expect(html).toContain("51 to 100 of 3641 tasks");
	});
	it("a search narrows the count", () => {
		query.q = { q: "bfcl-364" };
		const html = renderToStaticMarkup(<SuiteTasksTab suite={big} health={health} />);
		expect(html).toContain("1 to 2 of 2 matching tasks (3641 in all)");
	});
});
