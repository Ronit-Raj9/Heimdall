import type { SuiteTask } from "@/lib/types/bench";

// M6-1: client paging of the benchmark task table. A large benchmark (3,641 BFCL tasks) renders
// one page of rows, not all of them. Pure helpers, so the slice, the page clamp and the deep link
// to the page of ?task= are testable without a browser.

/** Rows per page of the task table. */
export const TASK_PAGE_SIZE = 50;

/** Tasks whose id or title contains the search text, case-insensitive. An empty search keeps all. */
export function searchTasks(tasks: readonly SuiteTask[], search: string | null | undefined): readonly SuiteTask[] {
	const needle = search?.trim().toLowerCase();
	if (!needle) return tasks;
	return tasks.filter((task) => task.id.toLowerCase().includes(needle) || (task.title ?? "").toLowerCase().includes(needle));
}

/** How many pages a list of total rows has; an empty list still has page 1. */
export function pageCount(total: number, size = TASK_PAGE_SIZE): number {
	return Math.max(1, Math.ceil(total / size));
}

/** The 1-based page that holds a task, or null when the task is not in the rows. */
export function pageOfTask(rows: readonly SuiteTask[], taskId: string | null, size = TASK_PAGE_SIZE): number | null {
	if (!taskId) return null;
	const index = rows.findIndex((task) => task.id === taskId);
	return index < 0 ? null : Math.floor(index / size) + 1;
}

export interface TaskPage {
	rows: readonly SuiteTask[];
	/** The page shown, 1-based, clamped into range. */
	page: number;
	pages: number;
	/** 1-based position of the first and last row shown; 0 and 0 for an empty list. */
	first: number;
	last: number;
	total: number;
}

/**
 * The page to show. The page in the URL wins; without one, a task opened from a link (?task=)
 * opens on the page that holds its row; otherwise page 1. An out-of-range page is clamped.
 */
export function taskPage(
	rows: readonly SuiteTask[],
	urlPage: number | null,
	openTask: string | null,
	size = TASK_PAGE_SIZE,
): TaskPage {
	const pages = pageCount(rows.length, size);
	const wanted = urlPage ?? pageOfTask(rows, openTask, size) ?? 1;
	const page = Math.min(Math.max(1, Math.floor(wanted)), pages);
	const start = (page - 1) * size;
	const slice = rows.slice(start, start + size);
	return {
		rows: slice,
		page,
		pages,
		first: slice.length > 0 ? start + 1 : 0,
		last: start + slice.length,
		total: rows.length,
	};
}
