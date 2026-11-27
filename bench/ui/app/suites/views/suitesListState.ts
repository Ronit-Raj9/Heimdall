import { SUITE_CATEGORIES, type SuiteCategory, type SuiteFilters, type SuiteSourceFilter } from "@/components/suites/suites.utils";
import { suiteDisplayName } from "@/components/suites/suiteDisplay";
import type { SuiteSummary } from "@/lib/types/bench";

// Pure helpers of the benchmarks list toolbar (M6-43): the URL fields read back into filters, and
// the sort orders. The list keeps q, category, source and sort in the URL so Back and reload keep them.

export type SuiteSort = "default" | "name" | "tasks" | "ready";
export const SUITE_SORTS: readonly SuiteSort[] = ["default", "name", "tasks", "ready"];

export interface SuiteUrlState {
	q: string | null;
	category: string | null;
	source: string | null;
	sort: string | null;
}

const SOURCES: readonly SuiteSourceFilter[] = ["all", "builtin", "team"];

/** The URL fields as filters; unknown values fall back to no filter. */
export function filtersFromUrl(s: SuiteUrlState): SuiteFilters {
	const category = SUITE_CATEGORIES.includes(s.category as SuiteCategory) ? (s.category as SuiteCategory) : null;
	const source = SOURCES.includes(s.source as SuiteSourceFilter) ? (s.source as SuiteSourceFilter) : "all";
	return { query: s.q ?? "", category, source };
}

/** The filters as URL fields; defaults are dropped so a clean list has a clean URL. */
export function filtersToUrl(f: Partial<SuiteFilters>): Partial<SuiteUrlState> {
	const out: Partial<SuiteUrlState> = {};
	if ("query" in f) out.q = f.query ? f.query : null;
	if ("category" in f) out.category = f.category ?? null;
	if ("source" in f) out.source = f.source && f.source !== "all" ? f.source : null;
	return out;
}

export function sortFromUrl(v: string | null): SuiteSort {
	return SUITE_SORTS.includes(v as SuiteSort) ? (v as SuiteSort) : "default";
}

type SortFields = Pick<SuiteSummary, "id" | "task_count" | "admitted_count" | "builtin" | "source">;

/** Share of tasks ready, 0 for a suite with no tasks. */
export function readyShare(s: Pick<SuiteSummary, "task_count" | "admitted_count">): number {
	return s.task_count > 0 ? (s.admitted_count ?? 0) / s.task_count : 0;
}

/** The list in the picked order; "default" keeps the server's order. Ties fall back to the name. */
export function sortSuites<T extends SortFields>(suites: readonly T[], sort: SuiteSort): T[] {
	if (sort === "default") return [...suites];
	const name = (s: T) => suiteDisplayName(s.id, s).toLowerCase();
	const byName = (a: T, b: T) => name(a).localeCompare(name(b));
	return [...suites].sort((a, b) => {
		if (sort === "tasks") return b.task_count - a.task_count || byName(a, b);
		if (sort === "ready") return readyShare(b) - readyShare(a) || byName(a, b);
		return byName(a, b);
	});
}
